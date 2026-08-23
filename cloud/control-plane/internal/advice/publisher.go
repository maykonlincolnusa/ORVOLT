package advice

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"google.golang.org/protobuf/proto"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
	advicev1 "github.com/orvolt/orvolt/contracts/gen/go/orvolt/control/advice/v1"
)

const StreamName = "ORVOLT_ADVICE"

// Publisher recomputes site advice on a timer and puts it on the bus.
//
// The consumer this is built for is site-local policy, per ADR-005: the cloud
// proposes, the site decides. Publishing rather than only answering an HTTP
// request matters because a site that has lost its uplink still holds the last
// proposal it received, and a proposal is exactly the kind of thing that should
// degrade to "the last thing I was told" rather than to nothing.
type Publisher struct {
	stream     jetstream.JetStream
	subject    string
	repository domain.Repository
	policy     Policy
	interval   time.Duration
	// activeWithin bounds which sites are worth computing for, and how recent a
	// connector reading must be to count as demand.
	activeWithin time.Duration
	now          func() time.Time
}

func NewPublisher(
	ctx context.Context,
	stream jetstream.JetStream,
	subject string,
	repository domain.Repository,
	policy Policy,
	interval time.Duration,
	retention time.Duration,
) (*Publisher, error) {
	_, err := stream.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:        StreamName,
		Description: "Advisory site charging capacity proposals. Never a command.",
		Subjects:    []string{subject, subject + ".>"},
		Storage:     jetstream.FileStorage,
		Retention:   jetstream.LimitsPolicy,
		MaxAge:      retention,
		MaxBytes:    1 << 30,
		Discard:     jetstream.DiscardOld,
		// Only the newest proposal per site matters: an hour-old opinion about
		// capacity is not evidence of anything and must never be replayed as
		// if it were current.
		MaxMsgsPerSubject: 1,
	})
	if err != nil {
		return nil, fmt.Errorf("create advice stream: %w", err)
	}
	return &Publisher{
		stream:       stream,
		subject:      subject,
		repository:   repository,
		policy:       policy,
		interval:     interval,
		activeWithin: 2 * time.Minute,
		now:          func() time.Time { return time.Now().UTC() },
	}, nil
}

func (publisher *Publisher) Name() string { return StreamName }

func (publisher *Publisher) Run(ctx context.Context) error {
	ticker := time.NewTicker(publisher.interval)
	defer ticker.Stop()
	slog.Info("charging advice publisher started", "interval", publisher.interval.String())

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := publisher.publishRound(ctx); err != nil && ctx.Err() == nil {
				slog.Warn("charging advice round failed", "error", err)
			}
		}
	}
}

func (publisher *Publisher) publishRound(ctx context.Context) error {
	sites, err := publisher.repository.ListActiveSites(ctx, publisher.activeWithin)
	if err != nil {
		return fmt.Errorf("list active sites: %w", err)
	}
	for _, site := range sites {
		if err := publisher.publishSite(ctx, site); err != nil {
			slog.Warn("publishing site advice failed", "site_id", site, "error", err)
		}
	}
	return nil
}

func (publisher *Publisher) publishSite(ctx context.Context, siteID string) error {
	connectors, err := publisher.repository.ListSiteDemand(ctx, siteID, publisher.activeWithin)
	if err != nil {
		return err
	}
	demands := make([]Demand, 0, len(connectors))
	for _, connector := range connectors {
		demands = append(demands, Demand{
			StationID:   connector.StationID,
			ConnectorID: connector.ConnectorID,
			RequestedKW: connector.PowerKW,
		})
	}

	var latest *domain.EnergyObservation
	if observation, found, err := publisher.repository.LatestEnergyObservation(ctx, siteID); err == nil && found {
		latest = &observation
	}

	computed := Compute(siteID, publisher.policy, latest, demands, publisher.now())
	payload, err := proto.Marshal(Encode(computed))
	if err != nil {
		return fmt.Errorf("encode advice: %w", err)
	}
	if _, err := publisher.stream.Publish(ctx, publisher.subject+"."+siteID, payload); err != nil {
		return fmt.Errorf("publish advice: %w", err)
	}
	return nil
}

var outcomes = map[string]advicev1.AllocationOutcome{
	OutcomeFull:    advicev1.AllocationOutcome_ALLOCATION_OUTCOME_FULL,
	OutcomeLimited: advicev1.AllocationOutcome_ALLOCATION_OUTCOME_LIMITED,
	OutcomePaused:  advicev1.AllocationOutcome_ALLOCATION_OUTCOME_PAUSED,
}

var bases = map[string]advicev1.AdviceBasis{
	BasisMeasured: advicev1.AdviceBasis_ADVICE_BASIS_MEASURED,
	BasisStale:    advicev1.AdviceBasis_ADVICE_BASIS_STALE,
	BasisNoData:   advicev1.AdviceBasis_ADVICE_BASIS_NO_DATA,
}

// Encode converts computed advice into the canonical wire message.
func Encode(computed Advice) *advicev1.SiteChargingAdvice {
	message := &advicev1.SiteChargingAdvice{
		SiteId:           computed.SiteID,
		ComputedAtMs:     computed.ComputedAt.UnixMilli(),
		SiteCapacityKw:   computed.SiteCapacityKW,
		NonEvLoadKw:      computed.NonEVLoadKW,
		AvailableForEvKw: computed.AvailableForEVKW,
		SolarSurplusKw:   computed.SolarSurplusKW,
		Basis:            bases[computed.Basis],
		ObservationAgeMs: int64(computed.ObservationAgeSec * 1000),
		Allocations:      make([]*advicev1.ConnectorAllocation, 0, len(computed.Allocations)),
	}
	for _, allocation := range computed.Allocations {
		message.Allocations = append(message.Allocations, &advicev1.ConnectorAllocation{
			StationId:   allocation.StationID,
			ConnectorId: allocation.ConnectorID,
			MaxPowerKw:  allocation.MaxPowerKW,
			Outcome:     outcomes[allocation.Outcome],
		})
	}
	return message
}
