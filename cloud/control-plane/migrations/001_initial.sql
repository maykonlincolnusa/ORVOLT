CREATE TABLE IF NOT EXISTS stations (
    station_id TEXT PRIMARY KEY,
    last_seen_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS connectors (
    station_id TEXT NOT NULL REFERENCES stations(station_id),
    connector_id TEXT NOT NULL,
    last_state TEXT NOT NULL,
    last_seen_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (station_id, connector_id)
);

CREATE TABLE IF NOT EXISTS telemetry (
    id BIGSERIAL PRIMARY KEY,
    station_id TEXT NOT NULL REFERENCES stations(station_id),
    connector_id TEXT NOT NULL,
    timestamp TIMESTAMPTZ NOT NULL,
    voltage DOUBLE PRECISION NOT NULL,
    current DOUBLE PRECISION NOT NULL,
    power_kw DOUBLE PRECISION NOT NULL,
    energy_kwh DOUBLE PRECISION NOT NULL,
    soc DOUBLE PRECISION NOT NULL,
    temperature_c DOUBLE PRECISION NOT NULL,
    state TEXT NOT NULL,
    edge_id TEXT NOT NULL,
    site_id TEXT NOT NULL,
    received_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (station_id, connector_id, timestamp, edge_id)
);

CREATE INDEX IF NOT EXISTS telemetry_station_timestamp_idx ON telemetry (station_id, timestamp DESC);
CREATE INDEX IF NOT EXISTS telemetry_received_at_idx ON telemetry (received_at DESC);
