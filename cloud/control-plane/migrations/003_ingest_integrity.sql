-- Device clocks are not trustworthy. Chargers boot without a battery-backed RTC
-- and before NTP converges, so a station can report 1970 or a date far in the
-- future. A single station claiming a future timestamp used to pin itself as
-- "latest" forever and poison station liveness.
--
-- This migration redefines every projection and ordering decision to use a
-- cloud-stamped arrival time, and keeps the device-reported instant as an
-- observation rather than as an index key.

ALTER TABLE telemetry
    ADD COLUMN IF NOT EXISTS ingested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    ADD COLUMN IF NOT EXISTS edge_sequence BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS clock_sync TEXT NOT NULL DEFAULT 'UNSPECIFIED';

ALTER TABLE energy_observations
    ADD COLUMN IF NOT EXISTS ingested_at TIMESTAMPTZ NOT NULL DEFAULT now();

-- "Latest" follows the order the control plane actually observed.
CREATE INDEX IF NOT EXISTS telemetry_station_ingested_idx
    ON telemetry (station_id, ingested_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS energy_observations_site_ingested_idx
    ON energy_observations (site_id, ingested_at DESC, id DESC);

-- stations.last_seen_at and connectors.last_seen_at now hold control-plane
-- arrival time, which is monotonic and therefore safe to compare against now().
-- What the station itself claimed is preserved separately for diagnosis.
ALTER TABLE stations
    ADD COLUMN IF NOT EXISTS last_device_time TIMESTAMPTZ;

UPDATE stations SET last_device_time = last_seen_at WHERE last_device_time IS NULL;

-- Silence detection scans stations by liveness, so it gets its own index.
CREATE INDEX IF NOT EXISTS stations_last_seen_idx ON stations (last_seen_at DESC);
