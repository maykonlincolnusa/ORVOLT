package ingest

import (
	"context"
	"testing"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/contract"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
)

func TestVerifierAcceptsMatchingIdentity(t *testing.T) {
	verify := VerifyTelemetryOrigin(telemetrySubject, true)
	err := verify(telemetrySubject+".edge-0001", domain.Telemetry{EdgeID: "edge-0001"})
	if err != nil {
		t.Fatalf("a device publishing under its own identity must be accepted: %v", err)
	}
}

// The attack this exists to stop: any device that can reach the bus writing
// another station's identity into its payload.
func TestVerifierRejectsImpersonation(t *testing.T) {
	verify := VerifyTelemetryOrigin(telemetrySubject, true)
	err := verify(telemetrySubject+".edge-0001", domain.Telemetry{EdgeID: "edge-9999"})
	if err == nil {
		t.Fatal("a device claiming another identity must be rejected")
	}
	if !contract.IsPermanent(err) {
		t.Fatalf("impersonation can never become valid on retry: %v", err)
	}
}

func TestUnidentifiedPublisherIsRejectedWhenIdentityIsRequired(t *testing.T) {
	verify := VerifyTelemetryOrigin(telemetrySubject, true)
	if err := verify(telemetrySubject, domain.Telemetry{EdgeID: "edge-0001"}); err == nil {
		t.Fatal("an unidentified publisher must be rejected when identity is required")
	}
}

// A development stack has no credentials and publishes to the bare subject.
func TestUnidentifiedPublisherIsAllowedWhenIdentityIsOptional(t *testing.T) {
	verify := VerifyTelemetryOrigin(telemetrySubject, false)
	if err := verify(telemetrySubject, domain.Telemetry{EdgeID: "edge-dev-001"}); err != nil {
		t.Fatalf("development publishers must still work: %v", err)
	}
}

// Even with identity optional, a device that *does* identify itself must not be
// able to claim a different one.
func TestMismatchIsRejectedEvenWhenIdentityIsOptional(t *testing.T) {
	verify := VerifyTelemetryOrigin(telemetrySubject, false)
	if err := verify(telemetrySubject+".edge-0001", domain.Telemetry{EdgeID: "edge-9999"}); err == nil {
		t.Fatal("a mismatched identity must be rejected regardless of strictness")
	}
}

// End to end through the batch loop: an impersonated message is parked, and the
// legitimate messages around it still make progress.
func TestProcessBatchRejectsImpersonatedTelemetry(t *testing.T) {
	var persisted []domain.Telemetry
	runner := newTelemetryTestRunner(func(_ context.Context, batch []domain.Telemetry) error {
		persisted = append(persisted, batch...)
		return nil
	}).WithVerifier(VerifyTelemetryOrigin(telemetrySubject, true))

	// validTelemetry builds records whose payload declares edge-1.
	honest := &fakeMessage{
		payload: validTelemetry(t, "station-a"),
		subject: telemetrySubject + ".edge-1",
	}
	impostor := &fakeMessage{
		payload: validTelemetry(t, "station-b"),
		subject: telemetrySubject + ".edge-2",
	}
	runner.ProcessBatch(context.Background(), []message{honest, impostor})

	if len(persisted) != 1 || persisted[0].StationID != "station-a" {
		t.Fatalf("only the authenticated record should persist, got %+v", persisted)
	}
	if honest.acked != 1 {
		t.Errorf("the honest message should be acknowledged, got %d", honest.acked)
	}
	if impostor.termed != 1 {
		t.Errorf("the impersonated message should be terminated, got term=%d", impostor.termed)
	}
	if impostor.acked != 0 {
		t.Error("an impersonated message must never be acknowledged as persisted")
	}
}
