package domain

import (
	"context"
	"time"
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
}

// EnergyObservation is a read-only external energy-site observation.
// Nil values were unavailable from the authorized provider, not measured as zero.
type EnergyObservation struct {
	SiteID                 string    `json:"site_id"`
	Provider               string    `json:"provider"`
	ProviderSiteID         string    `json:"provider_site_id"`
	ProviderAssetID        string    `json:"provider_asset_id,omitempty"`
	ConsentScope           string    `json:"consent_scope"`
	ObservedAt             time.Time `json:"observed_at"`
	RetrievedAt            time.Time `json:"retrieved_at"`
	SolarGenerationKW      *float64  `json:"solar_generation_kw,omitempty"`
	SiteLoadKW             *float64  `json:"site_load_kw,omitempty"`
	GridImportKW           *float64  `json:"grid_import_kw,omitempty"`
	GridExportKW           *float64  `json:"grid_export_kw,omitempty"`
	BatteryChargeKW        *float64  `json:"battery_charge_kw,omitempty"`
	BatteryDischargeKW     *float64  `json:"battery_discharge_kw,omitempty"`
	BatterySOC             *float64  `json:"battery_soc,omitempty"`
	TariffImportPerKWh     *float64  `json:"tariff_import_per_kwh,omitempty"`
	DataQuality            string    `json:"data_quality"`
}

type Connector struct {
	ConnectorID string    `json:"connector_id"`
	State       string    `json:"state"`
	LastSeenAt  time.Time `json:"last_seen_at"`
}

type Station struct {
	StationID   string      `json:"station_id"`
	LastSeenAt  time.Time   `json:"last_seen_at"`
	Connectors  []Connector `json:"connectors,omitempty"`
}

type Repository interface {
	PersistTelemetry(context.Context, Telemetry) error
	PersistEnergyObservation(context.Context, EnergyObservation) error
	ListStations(context.Context) ([]Station, error)
	GetStation(context.Context, string) (Station, bool, error)
	LatestTelemetry(context.Context, string) (Telemetry, bool, error)
	LatestEnergyObservation(context.Context, string) (EnergyObservation, bool, error)
}
