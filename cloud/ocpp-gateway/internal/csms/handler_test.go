package csms

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/lorenzodonini/ocpp-go/ocpp1.6/core"
	"github.com/lorenzodonini/ocpp-go/ocpp1.6/types"

	sessionv1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/session/evse/v1"
	evsev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/telemetry/evse/v1"
)

type recordingPublisher struct {
	telemetry []*evsev1.ChargingTelemetry
	sessions  []*sessionv1.ChargingSessionEvent
	failure   error
}

func (publisher *recordingPublisher) Telemetry(_ context.Context, telemetry *evsev1.ChargingTelemetry) error {
	if publisher.failure != nil {
		return publisher.failure
	}
	publisher.telemetry = append(publisher.telemetry, telemetry)
	return nil
}

func (publisher *recordingPublisher) Session(_ context.Context, event *sessionv1.ChargingSessionEvent) error {
	if publisher.failure != nil {
		return publisher.failure
	}
	publisher.sessions = append(publisher.sessions, event)
	return nil
}

type stubHasher struct{}

func (stubHasher) Hash(token string) string {
	if token == "" {
		return ""
	}
	return strings.Repeat("a", 64)
}

type fixedIDs struct{ value int }

func (ids fixedIDs) Next() int { return ids.value }

func newHandler(mode AuthorizationMode, publisher EventPublisher) *Handler {
	return NewHandler(
		Options{
			Origin:            Origin{GatewayID: "gw-1", SiteID: "site-1"},
			AuthorizationMode: mode,
			HeartbeatInterval: time.Minute,
			PublishTimeout:    time.Second,
		},
		publisher, stubHasher{}, fixedIDs{value: 900},
	)
}

func startRequest() *core.StartTransactionRequest {
	return &core.StartTransactionRequest{
		ConnectorId: 1,
		IdTag:       "04A1B2C3",
		MeterStart:  1_000,
		Timestamp:   types.NewDateTime(time.UnixMilli(1_800_000_000_000)),
	}
}

// Without an authorization service the gateway must not open a billable
// session, or anyone pointing a charger at this endpoint charges for free.
func TestStartTransactionIsRefusedWithoutAnAuthorizationService(t *testing.T) {
	publisher := &recordingPublisher{}
	handler := newHandler(DenyAll, publisher)

	confirmation, err := handler.OnStartTransaction("CP-1", startRequest())
	if err != nil {
		t.Fatalf("the refusal must be a protocol answer, not a transport error: %v", err)
	}
	if confirmation.IdTagInfo.Status != types.AuthorizationStatusInvalid {
		t.Errorf("expected the token to be refused, got %s", confirmation.IdTagInfo.Status)
	}
	if len(publisher.sessions) != 0 {
		t.Error("a refused transaction must not produce a session record")
	}
}

func TestStartTransactionOpensASession(t *testing.T) {
	publisher := &recordingPublisher{}
	handler := newHandler(AllowAll, publisher)

	confirmation, err := handler.OnStartTransaction("CP-1", startRequest())
	if err != nil {
		t.Fatal(err)
	}
	if confirmation.TransactionId != 900 {
		t.Errorf("expected the allocated transaction id, got %d", confirmation.TransactionId)
	}
	if len(publisher.sessions) != 1 {
		t.Fatalf("expected one session event, got %d", len(publisher.sessions))
	}

	event := publisher.sessions[0]
	if event.GetType() != sessionv1.SessionEventType_SESSION_EVENT_TYPE_STARTED {
		t.Errorf("expected a start event, got %s", event.GetType())
	}
	if event.GetSessionId() != SessionID("CP-1", 900) {
		t.Errorf("unexpected session id %q", event.GetSessionId())
	}
	if event.GetMeter().GetEnergyRegisterWh() != 1_000 {
		t.Errorf("the opening meter reading must be recorded, got %d", event.GetMeter().GetEnergyRegisterWh())
	}
}

// The card number is a credential and personal data. It must never leave the
// gateway in its original form.
func TestPresentedTokenIsNeverPublishedRaw(t *testing.T) {
	publisher := &recordingPublisher{}
	handler := newHandler(AllowAll, publisher)

	if _, err := handler.OnStartTransaction("CP-1", startRequest()); err != nil {
		t.Fatal(err)
	}
	hash := publisher.sessions[0].GetAuthorization().GetTokenHash()
	if hash == "04A1B2C3" || hash == "" {
		t.Fatalf("expected a hashed token, got %q", hash)
	}
}

// An OCPP charge point retries a transaction message it has not had accepted.
// Failing the call is therefore how the gateway avoids losing an invoice.
func TestStartTransactionFailsWhenTheSessionCannotBeRecorded(t *testing.T) {
	publisher := &recordingPublisher{failure: errors.New("event bus is unreachable")}
	handler := newHandler(AllowAll, publisher)

	if _, err := handler.OnStartTransaction("CP-1", startRequest()); err == nil {
		t.Fatal("expected the transaction to be refused so the charge point retries")
	}
}

func TestStopTransactionFailsWhenTheSessionCannotBeRecorded(t *testing.T) {
	publisher := &recordingPublisher{failure: errors.New("event bus is unreachable")}
	handler := newHandler(AllowAll, publisher)

	_, err := handler.OnStopTransaction("CP-1", &core.StopTransactionRequest{
		TransactionId: 900,
		MeterStop:     5_000,
		Timestamp:     types.NewDateTime(time.UnixMilli(1_800_000_100_000)),
		Reason:        core.ReasonLocal,
	})
	if err == nil {
		t.Fatal("expected the stop to be refused so the charge point retries")
	}
}

func TestStopTransactionClosesTheSameSession(t *testing.T) {
	publisher := &recordingPublisher{}
	handler := newHandler(AllowAll, publisher)

	if _, err := handler.OnStartTransaction("CP-1", startRequest()); err != nil {
		t.Fatal(err)
	}
	if _, err := handler.OnStopTransaction("CP-1", &core.StopTransactionRequest{
		TransactionId: 900,
		MeterStop:     5_000,
		Timestamp:     types.NewDateTime(time.UnixMilli(1_800_000_100_000)),
		Reason:        core.ReasonEVDisconnected,
	}); err != nil {
		t.Fatal(err)
	}

	if len(publisher.sessions) != 2 {
		t.Fatalf("expected a start and a stop, got %d events", len(publisher.sessions))
	}
	start, stop := publisher.sessions[0], publisher.sessions[1]
	if start.GetSessionId() != stop.GetSessionId() {
		t.Fatalf("the stop did not close the session it should have: %s vs %s",
			start.GetSessionId(), stop.GetSessionId())
	}
	if stop.GetStopReason() != sessionv1.StopReason_STOP_REASON_EV_DISCONNECTED {
		t.Errorf("stop reason was lost: %s", stop.GetStopReason())
	}
	// 5000 Wh closing minus 1000 Wh opening is what the control plane will bill.
	if stop.GetMeter().GetEnergyRegisterWh() != 5_000 {
		t.Errorf("closing meter reading was lost: %d", stop.GetMeter().GetEnergyRegisterWh())
	}
}

// Telemetry is an observation. Losing one must not make the charge point retry
// a connector state it has already left.
func TestStatusNotificationSucceedsEvenWhenPublishingFails(t *testing.T) {
	publisher := &recordingPublisher{failure: errors.New("event bus is unreachable")}
	handler := newHandler(AllowAll, publisher)

	if _, err := handler.OnStatusNotification("CP-1", &core.StatusNotificationRequest{
		ConnectorId: 1,
		Status:      core.ChargePointStatusCharging,
		ErrorCode:   core.NoError,
	}); err != nil {
		t.Fatalf("a status notification must not fail on a publish error: %v", err)
	}
}

func TestMeterValuesAdvanceTheSessionRegister(t *testing.T) {
	publisher := &recordingPublisher{}
	handler := newHandler(AllowAll, publisher)
	transaction := 900

	if _, err := handler.OnMeterValues("CP-1", &core.MeterValuesRequest{
		ConnectorId:   1,
		TransactionId: &transaction,
		MeterValue: []types.MeterValue{meterValue(
			sample(types.MeasurandEnergyActiveImportRegister, types.UnitOfMeasureWh, "3500"),
			sample(types.MeasurandPowerActiveImport, types.UnitOfMeasureW, "22000"),
		)},
	}); err != nil {
		t.Fatal(err)
	}

	if len(publisher.telemetry) != 1 {
		t.Fatalf("expected one telemetry event, got %d", len(publisher.telemetry))
	}
	if publisher.telemetry[0].GetPowerKw() != 22 {
		t.Errorf("power was not converted to kW: %v", publisher.telemetry[0].GetPowerKw())
	}
	if len(publisher.sessions) != 1 {
		t.Fatalf("expected one session update, got %d", len(publisher.sessions))
	}
	if publisher.sessions[0].GetMeter().GetEnergyRegisterWh() != 3_500 {
		t.Errorf("register was not carried into the session: %d",
			publisher.sessions[0].GetMeter().GetEnergyRegisterWh())
	}
}

// Meter values outside a transaction are still telemetry, but they must not
// invent a session.
func TestMeterValuesWithoutATransactionProduceNoSessionEvent(t *testing.T) {
	publisher := &recordingPublisher{}
	handler := newHandler(AllowAll, publisher)

	if _, err := handler.OnMeterValues("CP-1", &core.MeterValuesRequest{
		ConnectorId: 1,
		MeterValue: []types.MeterValue{meterValue(
			sample(types.MeasurandVoltage, types.UnitOfMeasureV, "400"))},
	}); err != nil {
		t.Fatal(err)
	}
	if len(publisher.sessions) != 0 {
		t.Errorf("expected no session events, got %d", len(publisher.sessions))
	}
	if len(publisher.telemetry) != 1 {
		t.Errorf("expected the reading to still be published, got %d", len(publisher.telemetry))
	}
}

// A charge point without a battery-backed clock learns the time from this
// answer, which is what keeps its later observations trustworthy.
func TestBootNotificationAnswersWithTheCurrentTime(t *testing.T) {
	handler := newHandler(AllowAll, &recordingPublisher{})
	confirmation, err := handler.OnBootNotification("CP-1", &core.BootNotificationRequest{
		ChargePointVendor: "ACME",
		ChargePointModel:  "Fast50",
	})
	if err != nil {
		t.Fatal(err)
	}
	if confirmation.Status != core.RegistrationStatusAccepted {
		t.Errorf("expected the charge point to be accepted, got %s", confirmation.Status)
	}
	if confirmation.CurrentTime == nil || confirmation.CurrentTime.IsZero() {
		t.Error("the boot answer must carry a usable time")
	}
	if confirmation.Interval != 60 {
		t.Errorf("expected the configured heartbeat interval in seconds, got %d", confirmation.Interval)
	}
}

func TestVendorDataTransferIsRejected(t *testing.T) {
	handler := newHandler(AllowAll, &recordingPublisher{})
	confirmation, err := handler.OnDataTransfer("CP-1", &core.DataTransferRequest{VendorId: "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if confirmation.Status != core.DataTransferStatusUnknownVendorId {
		t.Errorf("an unimplemented extension must not be reported as handled, got %s", confirmation.Status)
	}
}
