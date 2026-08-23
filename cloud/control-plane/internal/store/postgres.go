package store

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
)

type Postgres struct {
	pool *pgxpool.Pool
}

func Open(ctx context.Context, dsn string) (*Postgres, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("create PostgreSQL pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return &Postgres{pool: pool}, nil
}

func (postgres *Postgres) Close() { postgres.pool.Close() }

func (postgres *Postgres) Ping(ctx context.Context) error { return postgres.pool.Ping(ctx) }

func (postgres *Postgres) PersistTelemetry(ctx context.Context, telemetry domain.Telemetry) error {
	tx, err := postgres.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin telemetry transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
INSERT INTO stations (station_id, last_seen_at)
VALUES ($1, $2)
ON CONFLICT (station_id) DO UPDATE
SET last_seen_at = GREATEST(stations.last_seen_at, EXCLUDED.last_seen_at), updated_at = now()`, telemetry.StationID, telemetry.Timestamp); err != nil {
		return fmt.Errorf("upsert station: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO connectors (station_id, connector_id, last_state, last_seen_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (station_id, connector_id) DO UPDATE
SET last_state = EXCLUDED.last_state, last_seen_at = EXCLUDED.last_seen_at, updated_at = now()
WHERE EXCLUDED.last_seen_at >= connectors.last_seen_at`, telemetry.StationID, telemetry.ConnectorID, telemetry.State, telemetry.Timestamp); err != nil {
		return fmt.Errorf("upsert connector: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO telemetry (
  station_id, connector_id, timestamp, voltage, current, power_kw, energy_kwh, soc,
  temperature_c, state, edge_id, site_id, received_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13)
ON CONFLICT (station_id, connector_id, timestamp, edge_id) DO NOTHING`,
		telemetry.StationID, telemetry.ConnectorID, telemetry.Timestamp, telemetry.Voltage, telemetry.Current,
		telemetry.PowerKW, telemetry.EnergyKWh, telemetry.SOC, telemetry.TemperatureC, telemetry.State,
		telemetry.EdgeID, telemetry.SiteID, telemetry.ReceivedAt); err != nil {
		return fmt.Errorf("insert telemetry history: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit telemetry transaction: %w", err)
	}
	return nil
}

func (postgres *Postgres) PersistEnergyObservation(ctx context.Context, observation domain.EnergyObservation) error {
	tx, err := postgres.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin energy observation transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
INSERT INTO energy_sites (site_id, last_observed_at)
VALUES ($1, $2)
ON CONFLICT (site_id) DO UPDATE
SET last_observed_at = GREATEST(energy_sites.last_observed_at, EXCLUDED.last_observed_at), updated_at = now()`, observation.SiteID, observation.ObservedAt); err != nil {
		return fmt.Errorf("upsert energy site: %w", err)
	}
	if _, err := tx.Exec(ctx, `
INSERT INTO energy_observations (
  site_id, provider, provider_site_id, provider_asset_id, consent_scope, observed_at, retrieved_at,
  solar_generation_kw, site_load_kw, grid_import_kw, grid_export_kw, battery_charge_kw,
  battery_discharge_kw, battery_soc, tariff_import_per_kwh, data_quality
) VALUES (
  $1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
)
ON CONFLICT (site_id, provider, provider_site_id, provider_asset_id, observed_at) DO NOTHING`,
		observation.SiteID, observation.Provider, observation.ProviderSiteID, observation.ProviderAssetID,
		observation.ConsentScope, observation.ObservedAt, observation.RetrievedAt,
		observation.SolarGenerationKW, observation.SiteLoadKW, observation.GridImportKW,
		observation.GridExportKW, observation.BatteryChargeKW, observation.BatteryDischargeKW,
		observation.BatterySOC, observation.TariffImportPerKWh, observation.DataQuality); err != nil {
		return fmt.Errorf("insert energy observation history: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit energy observation transaction: %w", err)
	}
	return nil
}

func (postgres *Postgres) ListStations(ctx context.Context) ([]domain.Station, error) {
	rows, err := postgres.pool.Query(ctx, `SELECT station_id, last_seen_at FROM stations ORDER BY station_id`)
	if err != nil {
		return nil, fmt.Errorf("list stations: %w", err)
	}
	defer rows.Close()

	stations := make([]domain.Station, 0)
	for rows.Next() {
		var station domain.Station
		if err := rows.Scan(&station.StationID, &station.LastSeenAt); err != nil {
			return nil, fmt.Errorf("scan station: %w", err)
		}
		stations = append(stations, station)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate stations: %w", err)
	}
	return stations, nil
}

func (postgres *Postgres) GetStation(ctx context.Context, stationID string) (domain.Station, bool, error) {
	var station domain.Station
	err := postgres.pool.QueryRow(ctx, `SELECT station_id, last_seen_at FROM stations WHERE station_id = $1`, stationID).Scan(&station.StationID, &station.LastSeenAt)
	if err == pgx.ErrNoRows {
		return domain.Station{}, false, nil
	}
	if err != nil {
		return domain.Station{}, false, fmt.Errorf("get station: %w", err)
	}
	rows, err := postgres.pool.Query(ctx, `
SELECT connector_id, last_state, last_seen_at
FROM connectors WHERE station_id = $1 ORDER BY connector_id`, stationID)
	if err != nil {
		return domain.Station{}, false, fmt.Errorf("list connectors: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var connector domain.Connector
		if err := rows.Scan(&connector.ConnectorID, &connector.State, &connector.LastSeenAt); err != nil {
			return domain.Station{}, false, fmt.Errorf("scan connector: %w", err)
		}
		station.Connectors = append(station.Connectors, connector)
	}
	if err := rows.Err(); err != nil {
		return domain.Station{}, false, fmt.Errorf("iterate connectors: %w", err)
	}
	return station, true, nil
}

func (postgres *Postgres) LatestTelemetry(ctx context.Context, stationID string) (domain.Telemetry, bool, error) {
	row := postgres.pool.QueryRow(ctx, `
SELECT station_id, connector_id, timestamp, voltage, current, power_kw, energy_kwh, soc,
       temperature_c, state, edge_id, site_id, received_at
FROM telemetry WHERE station_id = $1 ORDER BY timestamp DESC, id DESC LIMIT 1`, stationID)
	var telemetry domain.Telemetry
	err := row.Scan(
		&telemetry.StationID, &telemetry.ConnectorID, &telemetry.Timestamp, &telemetry.Voltage,
		&telemetry.Current, &telemetry.PowerKW, &telemetry.EnergyKWh, &telemetry.SOC,
		&telemetry.TemperatureC, &telemetry.State, &telemetry.EdgeID, &telemetry.SiteID, &telemetry.ReceivedAt,
	)
	if err == pgx.ErrNoRows {
		return domain.Telemetry{}, false, nil
	}
	if err != nil {
		return domain.Telemetry{}, false, fmt.Errorf("get latest telemetry: %w", err)
	}
	return telemetry, true, nil
}

func (postgres *Postgres) LatestEnergyObservation(ctx context.Context, siteID string) (domain.EnergyObservation, bool, error) {
	row := postgres.pool.QueryRow(ctx, `
SELECT site_id, provider, provider_site_id, provider_asset_id, consent_scope, observed_at, retrieved_at,
       solar_generation_kw, site_load_kw, grid_import_kw, grid_export_kw, battery_charge_kw,
       battery_discharge_kw, battery_soc, tariff_import_per_kwh, data_quality
FROM energy_observations WHERE site_id = $1 ORDER BY observed_at DESC, id DESC LIMIT 1`, siteID)
	var observation domain.EnergyObservation
	var solarGeneration, siteLoad, gridImport, gridExport, batteryCharge, batteryDischarge, batterySOC, tariff pgtype.Float8
	err := row.Scan(
		&observation.SiteID, &observation.Provider, &observation.ProviderSiteID, &observation.ProviderAssetID,
		&observation.ConsentScope, &observation.ObservedAt, &observation.RetrievedAt,
		&solarGeneration, &siteLoad, &gridImport, &gridExport, &batteryCharge, &batteryDischarge,
		&batterySOC, &tariff, &observation.DataQuality,
	)
	if err == pgx.ErrNoRows {
		return domain.EnergyObservation{}, false, nil
	}
	if err != nil {
		return domain.EnergyObservation{}, false, fmt.Errorf("get latest energy observation: %w", err)
	}
	observation.SolarGenerationKW = optionalFloat(solarGeneration)
	observation.SiteLoadKW = optionalFloat(siteLoad)
	observation.GridImportKW = optionalFloat(gridImport)
	observation.GridExportKW = optionalFloat(gridExport)
	observation.BatteryChargeKW = optionalFloat(batteryCharge)
	observation.BatteryDischargeKW = optionalFloat(batteryDischarge)
	observation.BatterySOC = optionalFloat(batterySOC)
	observation.TariffImportPerKWh = optionalFloat(tariff)
	return observation, true, nil
}

func optionalFloat(value pgtype.Float8) *float64 {
	if !value.Valid {
		return nil
	}
	result := value.Float64
	return &result
}

var _ domain.Repository = (*Postgres)(nil)
