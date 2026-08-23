package domain

import (
	"context"
	"time"
)

// Clock synchronisation states reported by an edge agent. A charger that boots
// without a battery-backed RTC reports UNSYNCHRONIZED until its clock is
// trustworthy; consumers must not treat Timestamp as authoritative until then.
const (
	ClockSyncUnspecified    = "UNSPECIFIED"
	ClockSyncSynchronized   = "SYNCHRONIZED"
	ClockSyncUnsynchronized = "UNSYNCHRONIZED"
)

type Telemetry struct {
	StationID    string    `json:"station_id"`
	ConnectorID  string    `json:"connector_id"`
	Timestamp    time.Time `json:"timestamp"`
	Voltage      float64   `json:"voltage"`
	Current      float64   `json:"current"`
	PowerKW      float64   `json:"power_kw"`
	EnergyKWh    float64   `json:"energy_kwh"`
	SOC          float64   `json:"soc"`
	TemperatureC float64   `json:"temperature_c"`
	State        string    `json:"state"`
	EdgeID       string    `json:"edge_id"`
	SiteID       string    `json:"site_id"`
	ReceivedAt   time.Time `json:"received_at"`

	// EdgeSequence is the edge agent's monotonic counter, used to detect gaps
	// and to order observations sharing a wall-clock millisecond.
	EdgeSequence uint64 `json:"edge_sequence"`
	// ClockSync reports whether Timestamp and ReceivedAt can be trusted.
	ClockSync string `json:"clock_sync"`
	// IngestedAt is stamped by the control plane. It is the only monotonic,
	// trustworthy time in the record and is what projections order by.
	IngestedAt time.Time `json:"ingested_at"`
}

// EnergyObservation is a read-only external energy-site observation.
// Nil values were unavailable from the authorized provider, not measured as zero.
type EnergyObservation struct {
	SiteID             string    `json:"site_id"`
	Provider           string    `json:"provider"`
	ProviderSiteID     string    `json:"provider_site_id"`
	ProviderAssetID    string    `json:"provider_asset_id,omitempty"`
	ConsentScope       string    `json:"consent_scope"`
	ObservedAt         time.Time `json:"observed_at"`
	RetrievedAt        time.Time `json:"retrieved_at"`
	SolarGenerationKW  *float64  `json:"solar_generation_kw,omitempty"`
	SiteLoadKW         *float64  `json:"site_load_kw,omitempty"`
	GridImportKW       *float64  `json:"grid_import_kw,omitempty"`
	GridExportKW       *float64  `json:"grid_export_kw,omitempty"`
	BatteryChargeKW    *float64  `json:"battery_charge_kw,omitempty"`
	BatteryDischargeKW *float64  `json:"battery_discharge_kw,omitempty"`
	BatterySOC         *float64  `json:"battery_soc,omitempty"`
	TariffImportPerKWh *float64  `json:"tariff_import_per_kwh,omitempty"`
	DataQuality        string    `json:"data_quality"`
	IngestedAt         time.Time `json:"ingested_at"`
}

type Connector struct {
	ConnectorID string    `json:"connector_id"`
	State       string    `json:"state"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

type Station struct {
	StationID string `json:"station_id"`
	// LastSeenAt is the control plane's own arrival stamp, so it is safe to
	// compare against wall-clock now.
	LastSeenAt time.Time `json:"last_seen_at"`
	// LastDeviceTime is what the station itself claimed, kept for diagnosis.
	LastDeviceTime *time.Time  `json:"last_device_time,omitempty"`
	Connectors     []Connector `json:"connectors,omitempty"`
}

// StationHealth reports a station that has stopped reporting. A charger going
// dark is the primary operational signal in a charging network.
type StationHealth struct {
	StationID  string        `json:"station_id"`
	LastSeenAt time.Time     `json:"last_seen_at"`
	SilentFor  time.Duration `json:"-"`
	SilentForS float64       `json:"silent_for_seconds"`
}

// Page is a forward-only cursor. The fleet is far too large to serialise in one
// response, so listing is always bounded.
type Page struct {
	Limit int
	After string
}

const (
	DefaultPageLimit = 100
	MaxPageLimit     = 1000
)

// Normalize clamps a requested page into the supported range.
func (page Page) Normalize() Page {
	if page.Limit <= 0 {
		page.Limit = DefaultPageLimit
	}
	if page.Limit > MaxPageLimit {
		page.Limit = MaxPageLimit
	}
	return page
}

type Repository interface {
	PersistTelemetryBatch(context.Context, []Telemetry) error
	PersistEnergyObservationBatch(context.Context, []EnergyObservation) error
	ListStations(context.Context, Page) ([]Station, error)
	GetStation(context.Context, string) (Station, bool, error)
	LatestTelemetry(context.Context, string) (Telemetry, bool, error)
	LatestEnergyObservation(context.Context, string) (EnergyObservation, bool, error)
	ListSilentStations(context.Context, time.Duration) ([]StationHealth, error)
}
