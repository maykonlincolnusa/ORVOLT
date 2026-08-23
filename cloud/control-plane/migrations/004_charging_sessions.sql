-- Charging sessions.
--
-- Until now the system stored telemetry but had no concept of a transaction,
-- which is the entity every charging network actually runs on: it is what gets
-- billed, reported, reconciled and disputed. Telemetry is an observation of a
-- connector; a session is an accountable record of energy delivered to someone.

CREATE TABLE IF NOT EXISTS charging_sessions (
    session_id TEXT PRIMARY KEY,
    station_id TEXT NOT NULL,
    connector_id TEXT NOT NULL,
    site_id TEXT NOT NULL,
    source_protocol TEXT NOT NULL,
    transaction_reference TEXT NOT NULL DEFAULT '',

    -- Authorization is stored as a keyed hash. An RFID card number identifies a
    -- person and can be cloned; a stable hash still lets support and billing
    -- recognise the same card without the platform holding the credential.
    token_type TEXT NOT NULL DEFAULT 'UNSPECIFIED',
    token_hash TEXT NOT NULL DEFAULT '',
    authorization_reference TEXT NOT NULL DEFAULT '',

    started_at TIMESTAMPTZ,
    stopped_at TIMESTAMPTZ,

    -- Energy is derived from meter register readings rather than from a
    -- per-session accumulator, because a register is monotonic and survives a
    -- charge point reconnecting mid-session.
    meter_start_wh BIGINT,
    meter_last_wh BIGINT,
    meter_stop_wh BIGINT,

    stop_reason TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS charging_sessions_station_started_idx
    ON charging_sessions (station_id, started_at DESC);

-- Finding sessions that never closed is a daily operational task: they block
-- connectors and they are the usual cause of a billing dispute.
CREATE INDEX IF NOT EXISTS charging_sessions_open_idx
    ON charging_sessions (updated_at DESC)
    WHERE stopped_at IS NULL;

-- The append-only event log behind each session. Events can arrive out of
-- order or twice after a charge point reconnects, so the projection above is
-- rebuilt from these rather than trusted to arrive in sequence.
CREATE TABLE IF NOT EXISTS charging_session_events (
    id BIGSERIAL PRIMARY KEY,
    session_id TEXT NOT NULL,
    event_type TEXT NOT NULL,
    occurred_at TIMESTAMPTZ NOT NULL,
    ingested_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    energy_register_wh BIGINT,
    stop_reason TEXT NOT NULL DEFAULT '',
    source_protocol TEXT NOT NULL,
    UNIQUE (session_id, event_type, occurred_at)
);

CREATE INDEX IF NOT EXISTS charging_session_events_session_idx
    ON charging_session_events (session_id, occurred_at);
