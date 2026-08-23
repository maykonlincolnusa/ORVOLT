package ingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/contract"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
	evsev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/telemetry/evse/v1"
)

var ingestedAt = time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

const telemetrySubject = "orvolt.telemetry.evse.v1"

// fakeMessage records how the runner disposed of a message.
type fakeMessage struct {
	payload []byte
	subject string
	acked   int
	naked   int
	termed  int
}

func (message *fakeMessage) Data() []byte { return message.payload }
func (message *fakeMessage) Subject() string {
	if message.subject == "" {
		return telemetrySubject
	}
	return message.subject
}
func (message *fakeMessage) Ack() error { message.acked++; return nil }
func (message *fakeMessage) NakWithDelay(time.Duration) error {
	message.naked++
	return nil
}
func (message *fakeMessage) Term() error { message.termed++; return nil }

func newTelemetryTestRunner(persist Persister[domain.Telemetry]) *Runner[domain.Telemetry] {
	return &Runner[domain.Telemetry]{
		name:       TelemetryStreamName,
		decode:     contract.DecodeChargingTelemetry,
		persist:    persist,
		batchSize:  16,
		batchWait:  time.Millisecond,
		retryDelay: time.Millisecond,
		now:        func() time.Time { return ingestedAt },
	}
}

func validTelemetry(t *testing.T, stationID string) []byte {
	t.Helper()
	payload, err := proto.Marshal(&evsev1.ChargingTelemetry{
		StationId:    stationID,
		ConnectorId:  "1",
		TimestampMs:  1_700_000_000_000,
		Voltage:      400,
		Current:      75,
		PowerKw:      30,
		EnergyKwh:    20,
		Soc:          50,
		TemperatureC: 33,
		State:        evsev1.ChargingState_CHARGING_STATE_CHARGING,
		Edge: &evsev1.EdgeMetadata{
			EdgeId:       "edge-1",
			SiteId:       "site-1",
			ReceivedAtMs: 1_700_000_000_001,
			Sequence:     7,
			ClockSync:    evsev1.ClockSync_CLOCK_SYNC_SYNCHRONIZED,
		},
	})
	if err != nil {
		t.Fatalf("marshalling telemetry: %v", err)
	}
	return payload
}

func TestProcessBatchPersistsWholeBatchInOneCall(t *testing.T) {
	var batches [][]domain.Telemetry
	runner := newTelemetryTestRunner(func(_ context.Context, batch []domain.Telemetry) error {
		batches = append(batches, batch)
		return nil
	})

	messages := []message{
		&fakeMessage{payload: validTelemetry(t, "station-a")},
		&fakeMessage{payload: validTelemetry(t, "station-b")},
		&fakeMessage{payload: validTelemetry(t, "station-c")},
	}
	runner.ProcessBatch(context.Background(), messages)

	if len(batches) != 1 {
		t.Fatalf("expected exactly one persist call, got %d", len(batches))
	}
	if len(batches[0]) != 3 {
		t.Fatalf("expected 3 records in the batch, got %d", len(batches[0]))
	}
	for _, record := range batches[0] {
		if !record.IngestedAt.Equal(ingestedAt) {
			t.Errorf("every record in a batch must share one arrival stamp, got %s", record.IngestedAt)
		}
	}
	for index, received := range messages {
		fake := received.(*fakeMessage)
		if fake.acked != 1 || fake.naked != 0 || fake.termed != 0 {
			t.Errorf("message %d: expected exactly one ack, got ack=%d nak=%d term=%d",
				index, fake.acked, fake.naked, fake.termed)
		}
	}
}

func TestProcessBatchTerminatesUndecodablePayloads(t *testing.T) {
	persisted := 0
	runner := newTelemetryTestRunner(func(_ context.Context, batch []domain.Telemetry) error {
		persisted += len(batch)
		return nil
	})

	corrupt := &fakeMessage{payload: []byte{0xff, 0xff, 0xff, 0xff}}
	valid := &fakeMessage{payload: validTelemetry(t, "station-a")}
	runner.ProcessBatch(context.Background(), []message{corrupt, valid})

	// A payload that can never become valid must not be redelivered forever;
	// one corrupt device would otherwise stall the durable consumer.
	if corrupt.termed != 1 {
		t.Errorf("expected the corrupt payload to be terminated, got term=%d nak=%d", corrupt.termed, corrupt.naked)
	}
	if corrupt.acked != 0 {
		t.Errorf("a rejected payload must never be acknowledged as persisted")
	}
	// The rest of the batch still makes progress.
	if persisted != 1 || valid.acked != 1 {
		t.Errorf("expected the valid message to persist and ack, persisted=%d ack=%d", persisted, valid.acked)
	}
}

func TestProcessBatchRedeliversWhenPersistenceFails(t *testing.T) {
	runner := newTelemetryTestRunner(func(context.Context, []domain.Telemetry) error {
		return errors.New("database is unreachable")
	})

	messages := []*fakeMessage{
		{payload: validTelemetry(t, "station-a")},
		{payload: validTelemetry(t, "station-b")},
	}
	runner.ProcessBatch(context.Background(), []message{messages[0], messages[1]})

	for index, fake := range messages {
		if fake.acked != 0 {
			t.Errorf("message %d was acknowledged despite the write failing", index)
		}
		if fake.naked != 1 {
			t.Errorf("message %d: expected one redelivery request, got %d", index, fake.naked)
		}
		if fake.termed != 0 {
			t.Errorf("message %d: a transient failure must never terminate a message", index)
		}
	}
}

func TestProcessBatchIsANoOpWhenEverythingIsRejected(t *testing.T) {
	called := false
	runner := newTelemetryTestRunner(func(context.Context, []domain.Telemetry) error {
		called = true
		return nil
	})

	runner.ProcessBatch(context.Background(), []message{&fakeMessage{payload: []byte("not protobuf")}})

	if called {
		t.Error("the repository must not be called with an empty batch")
	}
}
