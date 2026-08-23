CREATE TABLE IF NOT EXISTS energy_sites (
    site_id TEXT PRIMARY KEY,
    last_observed_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS energy_observations (
    id BIGSERIAL PRIMARY KEY,
    site_id TEXT NOT NULL REFERENCES energy_sites(site_id),
    provider TEXT NOT NULL,
    provider_site_id TEXT NOT NULL,
    provider_asset_id TEXT NOT NULL DEFAULT '',
    consent_scope TEXT NOT NULL,
    observed_at TIMESTAMPTZ NOT NULL,
    retrieved_at TIMESTAMPTZ NOT NULL,
    solar_generation_kw DOUBLE PRECISION,
    site_load_kw DOUBLE PRECISION,
    grid_import_kw DOUBLE PRECISION,
    grid_export_kw DOUBLE PRECISION,
    battery_charge_kw DOUBLE PRECISION,
    battery_discharge_kw DOUBLE PRECISION,
    battery_soc DOUBLE PRECISION,
    tariff_import_per_kwh DOUBLE PRECISION,
    data_quality TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (site_id, provider, provider_site_id, provider_asset_id, observed_at)
);

CREATE INDEX IF NOT EXISTS energy_observations_site_timestamp_idx
    ON energy_observations (site_id, observed_at DESC);
