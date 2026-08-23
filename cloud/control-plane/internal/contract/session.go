package contract

import (
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
	sessionv1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/session/evse/v1"
)

var sessionEventTypes = map[sessionv1.SessionEventType]string{
	sessionv1.SessionEventType_SESSION_EVENT_TYPE_STARTED: domain.SessionStarted,
	sessionv1.SessionEventType_SESSION_EVENT_TYPE_UPDATED: domain.SessionUpdated,
	sessionv1.SessionEventType_SESSION_EVENT_TYPE_STOPPED: domain.SessionStopped,
}

var tokenTypes = map[sessionv1.TokenType]string{
	sessionv1.TokenType_TOKEN_TYPE_UNSPECIFIED:     "UNSPECIFIED",
	sessionv1.TokenType_TOKEN_TYPE_RFID:            "RFID",
	sessionv1.TokenType_TOKEN_TYPE_APP:             "APP",
	sessionv1.TokenType_TOKEN_TYPE_PLUG_AND_CHARGE: "PLUG_AND_CHARGE",
	sessionv1.TokenType_TOKEN_TYPE_REMOTE:          "REMOTE",
	sessionv1.TokenType_TOKEN_TYPE_LOCAL_FREE:      "LOCAL_FREE",
}

var stopReasons = map[sessionv1.StopReason]string{
	sessionv1.StopReason_STOP_REASON_UNSPECIFIED:          "",
	sessionv1.StopReason_STOP_REASON_LOCAL:                "LOCAL",
	sessionv1.StopReason_STOP_REASON_REMOTE:               "REMOTE",
	sessionv1.StopReason_STOP_REASON_EV_DISCONNECTED:      "EV_DISCONNECTED",
	sessionv1.StopReason_STOP_REASON_EMERGENCY_STOP:       "EMERGENCY_STOP",
	sessionv1.StopReason_STOP_REASON_POWER_LOSS:           "POWER_LOSS",
	sessionv1.StopReason_STOP_REASON_DE_AUTHORIZED:        "DE_AUTHORIZED",
	sessionv1.StopReason_STOP_REASON_ENERGY_LIMIT_REACHED: "ENERGY_LIMIT_REACHED",
	sessionv1.StopReason_STOP_REASON_TIME_LIMIT_REACHED:   "TIME_LIMIT_REACHED",
	sessionv1.StopReason_STOP_REASON_OTHER:                "OTHER",
}

var sourceProtocols = map[sessionv1.SourceProtocol]string{
	sessionv1.SourceProtocol_SOURCE_PROTOCOL_EDGE_AGENT: "EDGE_AGENT",
	sessionv1.SourceProtocol_SOURCE_PROTOCOL_OCPP_1_6:   "OCPP_1_6",
	sessionv1.SourceProtocol_SOURCE_PROTOCOL_OCPP_2_0_1: "OCPP_2_0_1",
}

func DecodeChargingSessionEvent(payload []byte, ingestedAt time.Time) (domain.SessionEvent, error) {
	var message sessionv1.ChargingSessionEvent
	if err := proto.Unmarshal(payload, &message); err != nil {
		return domain.SessionEvent{}, permanent("decode charging session event: %v", err)
	}
	if message.GetSessionId() == "" || message.GetStationId() == "" || message.GetOccurredAtMs() <= 0 {
		return domain.SessionEvent{}, permanent("session event is missing its identity or timestamp")
	}
	eventType, known := sessionEventTypes[message.GetType()]
	if !known {
		return domain.SessionEvent{}, permanent("session event type is unspecified or unknown")
	}
	protocol, known := sourceProtocols[message.GetSource()]
	if !known {
		return domain.SessionEvent{}, permanent("session event source protocol is unspecified or unknown")
	}

	event := domain.SessionEvent{
		SessionID:            message.GetSessionId(),
		StationID:            message.GetStationId(),
		ConnectorID:          message.GetConnectorId(),
		SiteID:               message.GetSiteId(),
		Type:                 eventType,
		OccurredAt:           time.UnixMilli(message.GetOccurredAtMs()).UTC(),
		IngestedAt:           ingestedAt,
		StopReason:           stopReasons[message.GetStopReason()],
		SourceProtocol:       protocol,
		TransactionReference: message.GetTransactionReference(),
	}

	if authorization := message.GetAuthorization(); authorization != nil {
		// A raw token would be personal data and a cloneable credential. The
		// adapter is required to hash it before publishing; a value that still
		// looks raw is rejected rather than quietly stored.
		if err := rejectRawToken(authorization.GetTokenHash()); err != nil {
			return domain.SessionEvent{}, err
		}
		event.TokenType = tokenTypes[authorization.GetTokenType()]
		event.TokenHash = authorization.GetTokenHash()
		event.AuthorizationRef = authorization.GetAuthorizationReference()
	}
	if event.TokenType == "" {
		event.TokenType = "UNSPECIFIED"
	}

	if meter := message.GetMeter(); meter != nil {
		register := meter.GetEnergyRegisterWh()
		if register < 0 {
			return domain.SessionEvent{}, permanent("energy register must not be negative")
		}
		event.EnergyRegisterWh = &register
	}

	return event, nil
}

// hexHashLength is the length of the hex-encoded HMAC-SHA256 the adapters emit.
const hexHashLength = 64

func rejectRawToken(hash string) error {
	if hash == "" {
		return nil
	}
	if len(hash) != hexHashLength {
		return permanent("authorization token must be a hex-encoded keyed hash, not a raw token")
	}
	for _, character := range hash {
		isHex := (character >= '0' && character <= '9') ||
			(character >= 'a' && character <= 'f')
		if !isHex {
			return permanent("authorization token must be a lowercase hex-encoded keyed hash")
		}
	}
	return nil
}
