package csms

import (
	"context"
	"errors"
	"log/slog"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"

	sessionv1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/session/evse/v1"
	evsev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/telemetry/evse/v1"
)

// AuthorizationMode decides what the gateway answers when a charge point asks
// whether a token may start a session.
//
// ORVOLT has no authorization service yet. Rather than silently approving every
// card, which would let anyone charge for free at any station pointed at this
// endpoint, the gateway declares which of the two honest answers it is giving.
type AuthorizationMode string

const (
	// DenyAll refuses every token. It is the default because it is the only
	// answer a system without an authorization service can give truthfully.
	DenyAll AuthorizationMode = "deny-all"
	// AllowAll accepts every token. Valid for a bench or a supervised pilot on
	// an isolated network, never for a station the public can reach.
	AllowAll AuthorizationMode = "allow-all"
)

// TokenHasher converts a presented credential into a storable identifier.
type TokenHasher interface {
	Hash(token string) string
}

// EventPublisher is the outward half of the gateway.
type EventPublisher interface {
	Telemetry(ctx context.Context, telemetry *evsev1.ChargingTelemetry) error
	Session(ctx context.Context, event *sessionv1.ChargingSessionEvent) error
}

// TransactionIDs allocates the OCPP transaction identifiers the gateway hands
// back to charge points.
//
// OCPP 1.6 makes the central system responsible for this number, and a charge
// point uses it to close the transaction later — possibly after a reconnect, a
// reboot, or a gateway restart. A production deployment must therefore back
// this with a shared, durable sequence (a PostgreSQL sequence or a NATS
// key-value bucket) so that two gateway replicas, or one gateway before and
// after a restart, can never issue the same number.
type TransactionIDs interface {
	Next() int
}

// MonotonicIDs is the in-process allocator.
//
// It is seeded from the wall clock so that a restart resumes above the previous
// run's range instead of colliding with it. That makes collisions unlikely, not
// impossible: it is adequate for a single gateway on a bench and explicitly not
// adequate for a replicated production deployment. See TransactionIDs.
type MonotonicIDs struct {
	counter atomic.Int64
}

func NewMonotonicIDs() *MonotonicIDs {
	allocator := &MonotonicIDs{}
	allocator.counter.Store(time.Now().Unix())
	return allocator
}

func (allocator *MonotonicIDs) Next() int {
	return int(allocator.counter.Add(1))
}

type Options struct {
	Origin            Origin
	AuthorizationMode AuthorizationMode
	// HeartbeatInterval is what the gateway asks charge points to use. It also
	// bounds how quickly a station going dark becomes visible.
	HeartbeatInterval time.Duration
	PublishTimeout    time.Duration
}

// Handler implements the OCPP 1.6 core profile.
type Handler struct {
	options     Options
	publisher   EventPublisher
	hasher      TokenHasher
	transaction TransactionIDs
	sequence    atomic.Uint64
	now         func() time.Time
}

func NewHandler(options Options, publisher EventPublisher, hasher TokenHasher, transaction TransactionIDs) *Handler {
	if options.HeartbeatInterval <= 0 {
		options.HeartbeatInterval = 5 * time.Minute
	}
	if options.PublishTimeout <= 0 {
		options.PublishTimeout = 10 * time.Second
	}
	if options.AuthorizationMode == "" {
		options.AuthorizationMode = DenyAll
	}
	return &Handler{
		options:     options,
		publisher:   publisher,
		hasher:      hasher,
		transaction: transaction,
		now:         func() time.Time { return time.Now().UTC() },
	}
}

func (handler *Handler) context() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), handler.options.PublishTimeout)
}

// OnBootNotification accepts the charge point and answers with the current
// time, which is how an OCPP charge point without a battery-backed clock learns
// what time it is. Answering accurately here directly reduces the number of
// observations that arrive flagged as unsynchronised.
func (handler *Handler) OnBootNotification(chargePointID string, request *core.BootNotificationRequest) (*core.BootNotificationConfirmation, error) {
	slog.Info("charge point booted",
		"charge_point_id", chargePointID,
		"vendor", request.ChargePointVendor,
		"model", request.ChargePointModel,
		"firmware", request.FirmwareVersion,
	)
	return core.NewBootNotificationConfirmation(
		types.NewDateTime(handler.now()),
		int(handler.options.HeartbeatInterval.Seconds()),
		core.RegistrationStatusAccepted,
	), nil
}

func (handler *Handler) OnHeartbeat(chargePointID string, _ *core.HeartbeatRequest) (*core.HeartbeatConfirmation, error) {
	slog.Debug("heartbeat", "charge_point_id", chargePointID)
	return core.NewHeartbeatConfirmation(types.NewDateTime(handler.now())), nil
}

// OnAuthorize answers the only question the gateway can answer honestly.
func (handler *Handler) OnAuthorize(chargePointID string, request *core.AuthorizeRequest) (*core.AuthorizeConfirmation, error) {
	status := types.AuthorizationStatusInvalid
	if handler.options.AuthorizationMode == AllowAll {
		status = types.AuthorizationStatusAccepted
	}
	slog.Info("authorization decision",
		"charge_point_id", chargePointID,
		"token", handler.hasher.Hash(request.IdTag),
		"mode", string(handler.options.AuthorizationMode),
		"status", string(status),
	)
	return core.NewAuthorizationConfirmation(types.NewIdTagInfo(status)), nil
}

// OnStatusNotification republishes a connector state change as telemetry.
func (handler *Handler) OnStatusNotification(chargePointID string, request *core.StatusNotificationRequest) (*core.StatusNotificationConfirmation, error) {
	state := ChargingStateOf(request.Status)
	if state == evsev1.ChargingState_CHARGING_STATE_UNSPECIFIED {
		// Refusing to guess is deliberate: an unknown status published as some
		// nearby state would be indistinguishable from a real reading.
		slog.Warn("unmapped connector status ignored",
			"charge_point_id", chargePointID, "status", string(request.Status))
		return core.NewStatusNotificationConfirmation(), nil
	}
	if request.ErrorCode != "" && request.ErrorCode != core.NoError {
		slog.Warn("charge point reported an error",
			"charge_point_id", chargePointID,
			"connector_id", request.ConnectorId,
			"error_code", string(request.ErrorCode),
			"info", request.Info,
			"vendor_error_code", request.VendorErrorCode,
		)
	}

	observedAt := handler.now()
	telemetry := TelemetryFrom(
		handler.options.Origin, chargePointID, request.ConnectorId, state, Reading{},
		timestampOr(request.Timestamp, observedAt), observedAt, handler.sequence.Add(1),
	)

	ctx, cancel := handler.context()
	defer cancel()
	if err := handler.publisher.Telemetry(ctx, telemetry); err != nil {
		// A status update is an observation, not billing evidence. Failing the
		// OCPP call would make the charge point retry a state it has already
		// left, so the loss is recorded instead of propagated.
		slog.Error("publishing status telemetry failed",
			"charge_point_id", chargePointID, "error", err)
	}
	return core.NewStatusNotificationConfirmation(), nil
}

// OnMeterValues republishes measurements, and advances the session's energy
// register when the samples belong to a transaction.
func (handler *Handler) OnMeterValues(chargePointID string, request *core.MeterValuesRequest) (*core.MeterValuesConfirmation, error) {
	ctx, cancel := handler.context()
	defer cancel()
	observedAt := handler.now()

	for _, value := range request.MeterValue {
		reading := MeterValueToReading(value)
		reportedAt := timestampOr(value.Timestamp, observedAt)

		telemetry := TelemetryFrom(
			handler.options.Origin, chargePointID, request.ConnectorId,
			evsev1.ChargingState_CHARGING_STATE_CHARGING, reading,
			reportedAt, observedAt, handler.sequence.Add(1),
		)
		if err := handler.publisher.Telemetry(ctx, telemetry); err != nil {
			slog.Error("publishing meter telemetry failed",
				"charge_point_id", chargePointID, "error", err)
		}

		if request.TransactionId == nil {
			continue
		}
		register, found := EnergyRegisterWh(value)
		if !found {
			continue
		}
		event := SessionEvent(
			handler.options.Origin, chargePointID, request.ConnectorId, *request.TransactionId,
			sessionv1.SessionEventType_SESSION_EVENT_TYPE_UPDATED, reportedAt,
			nil, &register, sessionv1.StopReason_STOP_REASON_UNSPECIFIED,
		)
		if err := handler.publisher.Session(ctx, event); err != nil {
			slog.Error("publishing session update failed",
				"charge_point_id", chargePointID, "error", err)
		}
	}
	return core.NewMeterValuesConfirmation(), nil
}

// OnStartTransaction opens a billable session.
//
// The event is published before the transaction id is returned. If publishing
// fails the call fails, so the charge point retries instead of running a
// session the platform has no record of.
func (handler *Handler) OnStartTransaction(chargePointID string, request *core.StartTransactionRequest) (*core.StartTransactionConfirmation, error) {
	if handler.options.AuthorizationMode == DenyAll {
		slog.Warn("refusing to start a transaction without an authorization service",
			"charge_point_id", chargePointID, "token", handler.hasher.Hash(request.IdTag))
		return core.NewStartTransactionConfirmation(
			types.NewIdTagInfo(types.AuthorizationStatusInvalid), 0), nil
	}

	transactionID := handler.transaction.Next()
	register := int64(request.MeterStart)
	occurredAt := timestampOr(request.Timestamp, handler.now())

	event := SessionEvent(
		handler.options.Origin, chargePointID, request.ConnectorId, transactionID,
		sessionv1.SessionEventType_SESSION_EVENT_TYPE_STARTED, occurredAt,
		&sessionv1.Authorization{
			TokenType: sessionv1.TokenType_TOKEN_TYPE_RFID,
			TokenHash: handler.hasher.Hash(request.IdTag),
		},
		&register, sessionv1.StopReason_STOP_REASON_UNSPECIFIED,
	)

	ctx, cancel := handler.context()
	defer cancel()
	if err := handler.publisher.Session(ctx, event); err != nil {
		slog.Error("publishing session start failed; refusing the transaction",
			"charge_point_id", chargePointID, "error", err)
		return nil, errors.New("session could not be recorded")
	}

	slog.Info("transaction started",
		"charge_point_id", chargePointID,
		"connector_id", request.ConnectorId,
		"transaction_id", transactionID,
		"session_id", SessionID(chargePointID, transactionID),
	)
	return core.NewStartTransactionConfirmation(
		types.NewIdTagInfo(types.AuthorizationStatusAccepted), transactionID), nil
}

// OnStopTransaction closes a billable session.
//
// Failing the call when the record cannot be published is the whole point: an
// OCPP charge point keeps retrying a StopTransaction it has not had accepted,
// so refusing preserves the transaction rather than losing an invoice.
func (handler *Handler) OnStopTransaction(chargePointID string, request *core.StopTransactionRequest) (*core.StopTransactionConfirmation, error) {
	ctx, cancel := handler.context()
	defer cancel()

	occurredAt := timestampOr(request.Timestamp, handler.now())
	register := int64(request.MeterStop)

	var authorization *sessionv1.Authorization
	if request.IdTag != "" {
		authorization = &sessionv1.Authorization{
			TokenType: sessionv1.TokenType_TOKEN_TYPE_RFID,
			TokenHash: handler.hasher.Hash(request.IdTag),
		}
	}

	event := SessionEvent(
		handler.options.Origin, chargePointID, connectorUnknown, request.TransactionId,
		sessionv1.SessionEventType_SESSION_EVENT_TYPE_STOPPED, occurredAt,
		authorization, &register, StopReasonOf(request.Reason),
	)
	if err := handler.publisher.Session(ctx, event); err != nil {
		slog.Error("publishing session stop failed; asking the charge point to retry",
			"charge_point_id", chargePointID, "transaction_id", request.TransactionId, "error", err)
		return nil, errors.New("session could not be recorded")
	}

	// Transaction data is the charge point's own record of the session. It is
	// published after the stop is safe, so a failure here cannot cost the
	// billable boundary reading.
	for _, value := range request.TransactionData {
		telemetry := TelemetryFrom(
			handler.options.Origin, chargePointID, 0,
			evsev1.ChargingState_CHARGING_STATE_FINISHING, MeterValueToReading(value),
			timestampOr(value.Timestamp, occurredAt), handler.now(), handler.sequence.Add(1),
		)
		if err := handler.publisher.Telemetry(ctx, telemetry); err != nil {
			slog.Warn("publishing transaction data failed",
				"charge_point_id", chargePointID, "error", err)
		}
	}

	slog.Info("transaction stopped",
		"charge_point_id", chargePointID,
		"transaction_id", request.TransactionId,
		"reason", string(request.Reason),
		"meter_stop_wh", request.MeterStop,
	)
	return core.NewStopTransactionConfirmation(), nil
}

// OnDataTransfer rejects vendor extensions. Accepting a message the gateway
// does not implement would tell the charge point its data was handled.
func (handler *Handler) OnDataTransfer(chargePointID string, request *core.DataTransferRequest) (*core.DataTransferConfirmation, error) {
	slog.Info("rejecting unsupported vendor data transfer",
		"charge_point_id", chargePointID, "vendor_id", request.VendorId)
	return core.NewDataTransferConfirmation(core.DataTransferStatusUnknownVendorId), nil
}

// connectorUnknown marks a StopTransaction, which carries no connector id in
// OCPP 1.6. The session projection already knows it from the start event.
const connectorUnknown = 0

func timestampOr(value *types.DateTime, fallback time.Time) time.Time {
	if value == nil || value.Time.IsZero() {
		return fallback
	}
	return value.Time.UTC()
}

// ConnectorLabel renders a connector id the way the canonical contract does.
func ConnectorLabel(connectorID int) string { return strconv.Itoa(connectorID) }
