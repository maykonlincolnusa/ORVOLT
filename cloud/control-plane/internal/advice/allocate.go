// Package advice computes how much power a site could give to charging.
//
// This is the point where the external energy data finally does something. It
// is also the point where a mistake would be expensive, so the rules are
// explicit:
//
//   - The output is advisory. Nothing here commands equipment. Per ADR-005 a
//     cloud-originated limit is a proposal that site-local policy, the EVSE
//     runtime and hardware limits must each be able to refuse.
//   - Missing or stale data reduces the proposal, never increases it. A
//     provider outage must cost optimisation, not safety margin.
//   - A share too small to charge with is not offered. Below roughly 1.4 kW a
//     vehicle stops accepting charge, so allocating less occupies capacity and
//     delivers nothing.
//
// The whole computation is a pure function of its inputs so that every one of
// those rules is directly testable.
package advice

import (
	"math"
	"sort"
	"time"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
)

// Basis records how much the advice can be trusted.
const (
	BasisMeasured = "MEASURED"
	BasisStale    = "STALE"
	BasisNoData   = "NO_DATA"
)

// Allocation outcomes.
const (
	OutcomeFull    = "FULL"
	OutcomeLimited = "LIMITED"
	OutcomePaused  = "PAUSED"
)

// MinimumUsefulKW is the smallest allocation worth making. A single-phase
// charger at the 6 A minimum most vehicles accept draws about this much;
// anything less and the vehicle stops charging.
const MinimumUsefulKW = 1.4

// Policy is the site's configuration. None of it is measured.
type Policy struct {
	// CapacityKW is the contracted or physical supply ceiling.
	CapacityKW float64
	// SafetyMarginKW is held back from every proposal, so that a measurement
	// error or an unmodelled load does not consume the last of the headroom.
	SafetyMarginKW float64
	// FallbackKW is proposed when energy data is missing or stale. It must be a
	// figure the site can sustain with no knowledge of its other loads.
	FallbackKW float64
	// ObservationFreshness bounds how old an energy observation may be before
	// it stops being evidence.
	ObservationFreshness time.Duration
	// MaxConnectorKW caps a single connector regardless of headroom.
	MaxConnectorKW float64
}

// Demand is one connector asking for power.
type Demand struct {
	StationID   string
	ConnectorID string
	// RequestedKW is what the connector would draw unconstrained. The station's
	// last observed charging power is the practical stand-in.
	RequestedKW float64
}

// Allocation is what one connector is advised to draw.
type Allocation struct {
	StationID   string  `json:"station_id"`
	ConnectorID string  `json:"connector_id"`
	MaxPowerKW  float64 `json:"max_power_kw"`
	Outcome     string  `json:"outcome"`
}

// Advice is the computed proposal for one site.
type Advice struct {
	SiteID            string       `json:"site_id"`
	ComputedAt        time.Time    `json:"computed_at"`
	SiteCapacityKW    float64      `json:"site_capacity_kw"`
	NonEVLoadKW       float64      `json:"non_ev_load_kw"`
	AvailableForEVKW  float64      `json:"available_for_ev_kw"`
	SolarSurplusKW    float64      `json:"solar_surplus_kw"`
	Basis             string       `json:"basis"`
	ObservationAgeSec float64      `json:"observation_age_seconds"`
	Allocations       []Allocation `json:"allocations"`
}

// Compute produces the proposal for one site.
//
// observation may be nil, which is the "no data" case rather than an error: a
// provider outage must degrade the proposal, not stop the site.
func Compute(
	siteID string,
	policy Policy,
	observation *domain.EnergyObservation,
	demands []Demand,
	now time.Time,
) Advice {
	advice := Advice{
		SiteID:         siteID,
		ComputedAt:     now,
		SiteCapacityKW: policy.CapacityKW,
		Allocations:    make([]Allocation, 0, len(demands)),
	}

	evPowerKW := 0.0
	for _, demand := range demands {
		evPowerKW += math.Max(demand.RequestedKW, 0)
	}

	switch {
	case observation == nil:
		advice.Basis = BasisNoData
		advice.AvailableForEVKW = policy.FallbackKW

	case now.Sub(observation.ObservedAt) > policy.ObservationFreshness:
		// The site's other loads may have changed since this was measured, so
		// the observation is evidence of nothing current.
		advice.Basis = BasisStale
		advice.ObservationAgeSec = now.Sub(observation.ObservedAt).Seconds()
		advice.AvailableForEVKW = policy.FallbackKW

	default:
		advice.Basis = BasisMeasured
		advice.ObservationAgeSec = now.Sub(observation.ObservedAt).Seconds()

		// Site load includes vehicle charging, so charging is subtracted to
		// isolate everything else. Adding the headroom back to the current
		// charging draw is what makes the result a total, not an increment.
		siteLoadKW := valueOr(observation.SiteLoadKW, 0)
		advice.NonEVLoadKW = math.Max(siteLoadKW-evPowerKW, 0)
		advice.AvailableForEVKW = policy.CapacityKW - advice.NonEVLoadKW

		if generation := observation.SolarGenerationKW; generation != nil {
			advice.SolarSurplusKW = math.Max(*generation-advice.NonEVLoadKW, 0)
		}
	}

	advice.AvailableForEVKW = math.Max(advice.AvailableForEVKW-policy.SafetyMarginKW, 0)
	advice.Allocations = allocate(advice.AvailableForEVKW, policy, demands)
	return advice
}

// allocate shares the available power between connectors.
//
// Connectors asking for less than their fair share are satisfied first and
// their remainder is redistributed, so a slow charger cannot hold capacity that
// a fast one could use. When the remaining budget cannot give every connector a
// usable amount, the smallest requests are paused rather than every connector
// being reduced to a trickle that charges nothing.
func allocate(availableKW float64, policy Policy, demands []Demand) []Allocation {
	allocations := make([]Allocation, 0, len(demands))
	if len(demands) == 0 {
		return allocations
	}

	// Deterministic order: the same inputs must always produce the same advice,
	// otherwise two runs disagree about who gets paused.
	ordered := make([]Demand, len(demands))
	copy(ordered, demands)
	sort.Slice(ordered, func(first, second int) bool {
		if ordered[first].RequestedKW != ordered[second].RequestedKW {
			return ordered[first].RequestedKW < ordered[second].RequestedKW
		}
		if ordered[first].StationID != ordered[second].StationID {
			return ordered[first].StationID < ordered[second].StationID
		}
		return ordered[first].ConnectorID < ordered[second].ConnectorID
	})

	results := make(map[int]Allocation, len(ordered))
	remainingKW := availableKW
	remaining := make([]int, 0, len(ordered))
	for index := range ordered {
		remaining = append(remaining, index)
	}

	// Satisfy modest requests first and recycle what they do not use.
	for len(remaining) > 0 {
		share := remainingKW / float64(len(remaining))
		satisfied := make([]int, 0, len(remaining))
		for _, index := range remaining {
			requested := clamp(ordered[index].RequestedKW, policy.MaxConnectorKW)
			if requested <= share {
				results[index] = Allocation{
					StationID:   ordered[index].StationID,
					ConnectorID: ordered[index].ConnectorID,
					MaxPowerKW:  round(requested),
					Outcome:     OutcomeFull,
				}
				remainingKW -= requested
				satisfied = append(satisfied, index)
			}
		}
		if len(satisfied) == 0 {
			break
		}
		remaining = without(remaining, satisfied)
	}

	// Whatever is left is shared, dropping connectors that cannot be given a
	// usable amount.
	for len(remaining) > 0 {
		share := remainingKW / float64(len(remaining))
		if share >= MinimumUsefulKW {
			for _, index := range remaining {
				results[index] = Allocation{
					StationID:   ordered[index].StationID,
					ConnectorID: ordered[index].ConnectorID,
					MaxPowerKW:  round(clamp(share, policy.MaxConnectorKW)),
					Outcome:     OutcomeLimited,
				}
			}
			remaining = nil
			break
		}
		// Pause the largest request: it is the one whose demand the remaining
		// budget is least able to serve.
		paused := remaining[len(remaining)-1]
		results[paused] = Allocation{
			StationID:   ordered[paused].StationID,
			ConnectorID: ordered[paused].ConnectorID,
			MaxPowerKW:  0,
			Outcome:     OutcomePaused,
		}
		remaining = remaining[:len(remaining)-1]
	}

	for index := range ordered {
		allocations = append(allocations, results[index])
	}
	return allocations
}

func without(indices []int, removed []int) []int {
	drop := make(map[int]struct{}, len(removed))
	for _, index := range removed {
		drop[index] = struct{}{}
	}
	kept := indices[:0:0]
	for _, index := range indices {
		if _, found := drop[index]; !found {
			kept = append(kept, index)
		}
	}
	return kept
}

func clamp(value, maximum float64) float64 {
	if maximum > 0 && value > maximum {
		return maximum
	}
	return math.Max(value, 0)
}

// round keeps the advice to a precision a charger can actually act on and
// prevents floating-point noise from making two identical inputs differ.
//
// It rounds *down*. These numbers are limits: rounding three connectors up to
// the nearest 10 W is enough to advise a total above what the site actually
// has, which is the one direction this function must never err in.
func round(value float64) float64 {
	return math.Floor(value*100) / 100
}

func valueOr(value *float64, fallback float64) float64 {
	if value == nil {
		return fallback
	}
	return *value
}
