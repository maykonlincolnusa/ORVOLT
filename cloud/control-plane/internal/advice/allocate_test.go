package advice

import (
	"math"
	"testing"
	"time"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
)

var now = time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

func policy() Policy {
	return Policy{
		CapacityKW:           100,
		SafetyMarginKW:       5,
		FallbackKW:           20,
		ObservationFreshness: 5 * time.Minute,
		MaxConnectorKW:       50,
	}
}

func observation(siteLoadKW float64, age time.Duration) *domain.EnergyObservation {
	load := siteLoadKW
	return &domain.EnergyObservation{
		SiteID:     "site-1",
		SiteLoadKW: &load,
		ObservedAt: now.Add(-age),
	}
}

func total(allocations []Allocation) float64 {
	sum := 0.0
	for _, allocation := range allocations {
		sum += allocation.MaxPowerKW
	}
	return sum
}

func find(t *testing.T, allocations []Allocation, connectorID string) Allocation {
	t.Helper()
	for _, allocation := range allocations {
		if allocation.ConnectorID == connectorID {
			return allocation
		}
	}
	t.Fatalf("connector %s has no allocation", connectorID)
	return Allocation{}
}

// The site's own load is subtracted from its capacity, and the charging that is
// already happening is added back so the result is a total rather than an
// increment.
func TestHeadroomExcludesTheSitesOtherLoads(t *testing.T) {
	demands := []Demand{{StationID: "a", ConnectorID: "1", RequestedKW: 22}}
	// 60 kW measured at the site, of which 22 kW is this charger: 38 kW is
	// everything else. 100 - 38 - 5 margin = 57 kW for charging.
	result := Compute("site-1", policy(), observation(60, time.Minute), demands, now)

	if result.Basis != BasisMeasured {
		t.Fatalf("expected a measured basis, got %s", result.Basis)
	}
	if math.Abs(result.NonEVLoadKW-38) > 1e-9 {
		t.Errorf("expected 38 kW of non-EV load, got %v", result.NonEVLoadKW)
	}
	if math.Abs(result.AvailableForEVKW-57) > 1e-9 {
		t.Errorf("expected 57 kW available, got %v", result.AvailableForEVKW)
	}
}

// The core safety property: a missing observation must never be read as an
// empty site with all its capacity free.
func TestMissingObservationFallsBackConservatively(t *testing.T) {
	result := Compute("site-1", policy(), nil,
		[]Demand{{StationID: "a", ConnectorID: "1", RequestedKW: 50}}, now)

	if result.Basis != BasisNoData {
		t.Fatalf("expected a no-data basis, got %s", result.Basis)
	}
	// Fallback 20 minus the 5 kW margin.
	if math.Abs(result.AvailableForEVKW-15) > 1e-9 {
		t.Errorf("expected the conservative fallback, got %v kW", result.AvailableForEVKW)
	}
	if result.AvailableForEVKW >= policy().CapacityKW {
		t.Error("an unknown site must never be treated as an empty one")
	}
}

func TestStaleObservationFallsBackConservatively(t *testing.T) {
	// A ten-minute-old reading of a site whose other loads may have changed.
	result := Compute("site-1", policy(), observation(10, 10*time.Minute),
		[]Demand{{StationID: "a", ConnectorID: "1", RequestedKW: 50}}, now)

	if result.Basis != BasisStale {
		t.Fatalf("expected a stale basis, got %s", result.Basis)
	}
	if math.Abs(result.AvailableForEVKW-15) > 1e-9 {
		t.Errorf("a stale reading must not authorise its headroom, got %v kW", result.AvailableForEVKW)
	}
	if result.ObservationAgeSec != 600 {
		t.Errorf("expected the age to be reported, got %v", result.ObservationAgeSec)
	}
}

// A heavily loaded site can leave nothing for charging, and the proposal must
// say so rather than going negative.
func TestOverloadedSiteOffersNothing(t *testing.T) {
	result := Compute("site-1", policy(), observation(140, time.Minute),
		[]Demand{{StationID: "a", ConnectorID: "1", RequestedKW: 22}}, now)

	if result.AvailableForEVKW != 0 {
		t.Fatalf("expected no headroom, got %v kW", result.AvailableForEVKW)
	}
	if find(t, result.Allocations, "1").Outcome != OutcomePaused {
		t.Error("with no headroom the connector must be paused")
	}
}

// The allocation must never exceed what the site was told it has.
func TestAllocationsNeverExceedAvailablePower(t *testing.T) {
	demands := []Demand{
		{StationID: "a", ConnectorID: "1", RequestedKW: 50},
		{StationID: "a", ConnectorID: "2", RequestedKW: 50},
		{StationID: "b", ConnectorID: "1", RequestedKW: 50},
	}
	result := Compute("site-1", policy(), observation(70, time.Minute), demands, now)

	if total(result.Allocations) > result.AvailableForEVKW+1e-6 {
		t.Fatalf("allocated %v kW from %v kW available",
			total(result.Allocations), result.AvailableForEVKW)
	}
}

// A slow charger must not hold capacity a fast one could use.
func TestUnusedShareIsRedistributed(t *testing.T) {
	demands := []Demand{
		{StationID: "a", ConnectorID: "slow", RequestedKW: 3},
		{StationID: "a", ConnectorID: "fast", RequestedKW: 50},
	}
	// No other site load: 100 - 0 - 5 = 95 kW, so both fit entirely.
	result := Compute("site-1", policy(), observation(53, time.Minute), demands, now)

	slow := find(t, result.Allocations, "slow")
	fast := find(t, result.Allocations, "fast")
	if slow.Outcome != OutcomeFull || slow.MaxPowerKW != 3 {
		t.Errorf("the modest request should be satisfied in full, got %+v", slow)
	}
	if fast.MaxPowerKW <= result.AvailableForEVKW/2 {
		t.Errorf("the fast charger should receive more than an equal split, got %v kW", fast.MaxPowerKW)
	}
}

// The rule that makes the difference between load management and a system that
// leaves every car not charging: a share too small to charge with is worse than
// none, because it occupies capacity and delivers nothing.
func TestConnectorsThatCannotChargeUsefullyArePausedRatherThanTrickled(t *testing.T) {
	demands := []Demand{
		{StationID: "a", ConnectorID: "1", RequestedKW: 22},
		{StationID: "a", ConnectorID: "2", RequestedKW: 22},
		{StationID: "a", ConnectorID: "3", RequestedKW: 22},
	}
	// The three connectors draw 66 kW of the site's 158 kW, so 92 kW is other
	// load. 100 - 92 - 5 margin leaves 3 kW: enough for two connectors at
	// 1.5 kW, not for three at 1.0 kW.
	result := Compute("site-1", policy(), observation(158, time.Minute), demands, now)

	paused, charging := 0, 0
	for _, allocation := range result.Allocations {
		if allocation.Outcome == OutcomePaused {
			paused++
			continue
		}
		charging++
		if allocation.MaxPowerKW < MinimumUsefulKW {
			t.Errorf("connector %s was given an unusable %v kW",
				allocation.ConnectorID, allocation.MaxPowerKW)
		}
	}
	if paused == 0 {
		t.Fatal("expected at least one connector to be paused instead of trickled")
	}
	if charging == 0 {
		t.Fatal("expected the remaining budget to be usable by somebody")
	}
}

func TestConnectorCapIsRespected(t *testing.T) {
	limited := policy()
	limited.MaxConnectorKW = 11
	result := Compute("site-1", limited, observation(0, time.Minute),
		[]Demand{{StationID: "a", ConnectorID: "1", RequestedKW: 50}}, now)

	if find(t, result.Allocations, "1").MaxPowerKW != 11 {
		t.Fatalf("the per-connector cap was not applied: %+v", result.Allocations)
	}
}

func TestSolarSurplusIsWhatGenerationExceedsTheSitesOwnLoad(t *testing.T) {
	generation := 40.0
	measurement := observation(60, time.Minute)
	measurement.SolarGenerationKW = &generation

	// 60 kW site load includes 22 kW of charging, so 38 kW is other load.
	// 40 kW generated minus 38 kW consumed leaves 2 kW of surplus.
	result := Compute("site-1", policy(), measurement,
		[]Demand{{StationID: "a", ConnectorID: "1", RequestedKW: 22}}, now)

	if math.Abs(result.SolarSurplusKW-2) > 1e-9 {
		t.Fatalf("expected 2 kW of surplus, got %v", result.SolarSurplusKW)
	}
}

func TestNoDemandsProduceNoAllocations(t *testing.T) {
	result := Compute("site-1", policy(), observation(10, time.Minute), nil, now)
	if len(result.Allocations) != 0 {
		t.Fatalf("expected no allocations, got %d", len(result.Allocations))
	}
}

// Two runs over the same inputs must agree about who is paused, or chargers
// would oscillate.
func TestAdviceIsDeterministic(t *testing.T) {
	demands := []Demand{
		{StationID: "b", ConnectorID: "1", RequestedKW: 22},
		{StationID: "a", ConnectorID: "2", RequestedKW: 22},
		{StationID: "a", ConnectorID: "1", RequestedKW: 22},
	}
	first := Compute("site-1", policy(), observation(158, time.Minute), demands, now)
	second := Compute("site-1", policy(), observation(158, time.Minute), demands, now)

	if len(first.Allocations) != len(second.Allocations) {
		t.Fatal("allocation count differed between identical runs")
	}
	for index := range first.Allocations {
		if first.Allocations[index] != second.Allocations[index] {
			t.Fatalf("run %d differed: %+v vs %+v",
				index, first.Allocations[index], second.Allocations[index])
		}
	}
}
