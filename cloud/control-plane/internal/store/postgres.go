package store

import (
	"context"
	"errors"
	"fmt"
	"time"

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

const (
	upsertStationSQL = `
INSERT INTO stations (station_id, last_seen_at, last_device_time)
VALUES ($1, $2, $3)
ON CONFLICT (station_id) DO UPDATE
SET last_seen_at = GREATEST(stations.last_seen_at, EXCLUDED.last_seen_at),
    last_device_time = EXCLUDED.last_device_time,
    updated_at = now()`

	upsertConnectorSQL = `
INSERT INTO connectors (station_id, connector_id, last_state, last_seen_at)
VALUES ($1, $2, $3, $4)
ON CONFLICT (station_id, connector_id) DO UPDATE
SET last_state = EXCLUDED.last_state, last_seen_at = EXCLUDED.last_seen_at, updated_at = now()
WHERE EXCLUDED.last_seen_at >= connectors.last_seen_at`

	insertTelemetrySQL = `
INSERT INTO telemetry (
  station_id, connector_id, timestamp, voltage, current, power_kw, energy_kwh, soc,
  temperature_c, state, edge_id, site_id, received_at, ingested_at, edge_sequence, clock_sync
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)
ON CONFLICT (station_id, connector_id, timestamp, edge_id) DO NOTHING`

	upsertEnergySiteSQL = `
INSERT INTO energy_sites (site_id, last_observed_at)
VALUES ($1, $2)
ON CONFLICT (site_id) DO UPDATE
SET last_observed_at = GREATEST(energy_sites.last_observed_at, EXCLUDED.last_observed_at),
    updated_at = now()`

	insertEnergyObservationSQL = `
INSERT INTO energy_observations (
  site_id, provider, provider_site_id, provider_asset_id, consent_scope, observed_at, retrieved_at,
  solar_generation_kw, site_load_kw, grid_import_kw, grid_export_kw, battery_charge_kw,
  battery_discharge_kw, battery_soc, tariff_import_per_kwh, data_quality, ingested_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)
ON CONFLICT (site_id, provider, provider_site_id, provider_asset_id, observed_at) DO NOTHING`
)

// PersistTelemetryBatch writes a whole ingest batch in one transaction.
//
// The projections are keyed on ingested_at, which the control plane stamps, so
// a station with a wrong clock cannot corrupt station or connector liveness.
// Every statement is idempotent because JetStream delivery is at-least-once.
func (postgres *Postgres) PersistTelemetryBatch(ctx context.Context, telemetry []domain.Telemetry) error {
	if len(telemetry) == 0 {
		return nil
	}
	return postgres.inTransaction(ctx, func(tx pgx.Tx) error {
		batch := &pgx.Batch{}
		for _, record := range telemetry {
			batch.Queue(upsertStationSQL, record.StationID, record.IngestedAt, record.Timestamp)
			batch.Queue(upsertConnectorSQL, record.StationID, record.ConnectorID, record.State, record.IngestedAt)
			batch.Queue(insertTelemetrySQL,
				record.StationID, record.ConnectorID, record.Timestamp, record.Voltage, record.Current,
				record.PowerKW, record.EnergyKWh, record.SOC, record.TemperatureC, record.State,
				record.EdgeID, record.SiteID, record.ReceivedAt, record.IngestedAt,
				int64(record.EdgeSequence), record.ClockSync)
		}
		return execBatch(ctx, tx, batch, "telemetry")
	})
}

func (postgres *Postgres) PersistEnergyObservationBatch(ctx context.Context, observations []domain.EnergyObservation) error {
	if len(observations) == 0 {
		return nil
	}
	return postgres.inTransaction(ctx, func(tx pgx.Tx) error {
		batch := &pgx.Batch{}
		for _, record := range observations {
			batch.Queue(upsertEnergySiteSQL, record.SiteID, record.ObservedAt)
			batch.Queue(insertEnergyObservationSQL,
				record.SiteID, record.Provider, record.ProviderSiteID, record.ProviderAssetID,
				record.ConsentScope, record.ObservedAt, record.RetrievedAt,
				record.SolarGenerationKW, record.SiteLoadKW, record.GridImportKW,
				record.GridExportKW, record.BatteryChargeKW, record.BatteryDischargeKW,
				record.BatterySOC, record.TariffImportPerKWh, record.DataQuality, record.IngestedAt)
		}
		return execBatch(ctx, tx, batch, "energy observation")
	})
}

func (postgres *Postgres) inTransaction(ctx context.Context, body func(pgx.Tx) error) error {
	tx, err := postgres.pool.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if err := body(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func execBatch(ctx context.Context, tx pgx.Tx, batch *pgx.Batch, label string) error {
	results := tx.SendBatch(ctx, batch)
	for index := 0; index < batch.Len(); index++ {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return fmt.Errorf("execute %s statement %d/%d: %w", label, index+1, batch.Len(), err)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("close %s batch: %w", label, err)
	}
	return nil
}

func (postgres *Postgres) ListStations(ctx context.Context, page domain.Page) ([]domain.Station, error) {
	page = page.Normalize()
	rows, err := postgres.pool.Query(ctx, `
SELECT station_id, last_seen_at, last_device_time
FROM stations
WHERE station_id > $1
ORDER BY station_id
LIMIT $2`, page.After, page.Limit)
	if err != nil {
		return nil, fmt.Errorf("list stations: %w", err)
	}
	defer rows.Close()

	stations := make([]domain.Station, 0, page.Limit)
	for rows.Next() {
		var station domain.Station
		var deviceTime pgtype.Timestamptz
		if err := rows.Scan(&station.StationID, &station.LastSeenAt, &deviceTime); err != nil {
			return nil, fmt.Errorf("scan station: %w", err)
		}
		if deviceTime.Valid {
			value := deviceTime.Time.UTC()
			station.LastDeviceTime = &value
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
	var deviceTime pgtype.Timestamptz
	err := postgres.pool.QueryRow(ctx, `
SELECT station_id, last_seen_at, last_device_time
FROM stations WHERE station_id = $1`, stationID).Scan(&station.StationID, &station.LastSeenAt, &deviceTime)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Station{}, false, nil
	}
	if err != nil {
		return domain.Station{}, false, fmt.Errorf("get station: %w", err)
	}
	if deviceTime.Valid {
		value := deviceTime.Time.UTC()
		station.LastDeviceTime = &value
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

// LatestTelemetry orders by the control plane's arrival stamp rather than by
// the device timestamp, so a charger with a wrong clock cannot pin itself as
// the newest record forever.
func (postgres *Postgres) LatestTelemetry(ctx context.Context, stationID string) (domain.Telemetry, bool, error) {
	row := postgres.pool.QueryRow(ctx, `
SELECT station_id, connector_id, timestamp, voltage, current, power_kw, energy_kwh, soc,
       temperature_c, state, edge_id, site_id, received_at, ingested_at, edge_sequence, clock_sync
FROM telemetry WHERE station_id = $1 ORDER BY ingested_at DESC, id DESC LIMIT 1`, stationID)
	var telemetry domain.Telemetry
	var sequence int64
	err := row.Scan(
		&telemetry.StationID, &telemetry.ConnectorID, &telemetry.Timestamp, &telemetry.Voltage,
		&telemetry.Current, &telemetry.PowerKW, &telemetry.EnergyKWh, &telemetry.SOC,
		&telemetry.TemperatureC, &telemetry.State, &telemetry.EdgeID, &telemetry.SiteID,
		&telemetry.ReceivedAt, &telemetry.IngestedAt, &sequence, &telemetry.ClockSync,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Telemetry{}, false, nil
	}
	if err != nil {
		return domain.Telemetry{}, false, fmt.Errorf("get latest telemetry: %w", err)
	}
	telemetry.EdgeSequence = uint64(sequence)
	return telemetry, true, nil
}

func (postgres *Postgres) LatestEnergyObservation(ctx context.Context, siteID string) (domain.EnergyObservation, bool, error) {
	row := postgres.pool.QueryRow(ctx, `
SELECT site_id, provider, provider_site_id, provider_asset_id, consent_scope, observed_at, retrieved_at,
       solar_generation_kw, site_load_kw, grid_import_kw, grid_export_kw, battery_charge_kw,
       battery_discharge_kw, battery_soc, tariff_import_per_kwh, data_quality, ingested_at
FROM energy_observations WHERE site_id = $1 ORDER BY ingested_at DESC, id DESC LIMIT 1`, siteID)
	var observation domain.EnergyObservation
	var solarGeneration, siteLoad, gridImport, gridExport, batteryCharge, batteryDischarge, batterySOC, tariff pgtype.Float8
	err := row.Scan(
		&observation.SiteID, &observation.Provider, &observation.ProviderSiteID, &observation.ProviderAssetID,
		&observation.ConsentScope, &observation.ObservedAt, &observation.RetrievedAt,
		&solarGeneration, &siteLoad, &gridImport, &gridExport, &batteryCharge, &batteryDischarge,
		&batterySOC, &tariff, &observation.DataQuality, &observation.IngestedAt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
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

// ListSilentStations reports stations that stopped reporting. A charger going
// dark is the primary operational signal in a charging network, and it is
// invisible to any check that only looks at the data that did arrive.
func (postgres *Postgres) ListSilentStations(ctx context.Context, threshold time.Duration) ([]domain.StationHealth, error) {
	rows, err := postgres.pool.Query(ctx, `
SELECT station_id, last_seen_at, now() - last_seen_at
FROM stations
WHERE last_seen_at < now() - $1::interval
ORDER BY last_seen_at`, threshold)
	if err != nil {
		return nil, fmt.Errorf("list silent stations: %w", err)
	}
	defer rows.Close()

	silent := make([]domain.StationHealth, 0)
	for rows.Next() {
		var station domain.StationHealth
		var interval pgtype.Interval
		if err := rows.Scan(&station.StationID, &station.LastSeenAt, &interval); err != nil {
			return nil, fmt.Errorf("scan silent station: %w", err)
		}
		station.SilentFor = intervalDuration(interval)
		station.SilentForS = station.SilentFor.Seconds()
		silent = append(silent, station)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate silent stations: %w", err)
	}
	return silent, nil
}

func intervalDuration(interval pgtype.Interval) time.Duration {
	if !interval.Valid {
		return 0
	}
	return time.Duration(interval.Microseconds)*time.Microsecond +
		time.Duration(interval.Days)*24*time.Hour +
		time.Duration(interval.Months)*30*24*time.Hour
}

func optionalFloat(value pgtype.Float8) *float64 {
	if !value.Valid {
		return nil
	}
	result := value.Float64
	return &result
}

var _ domain.Repository = (*Postgres)(nil)
