package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
)

// insertSessionEventSQL keeps the immutable log. The unique constraint makes a
// redelivered event a no-op rather than a duplicate.
const insertSessionEventSQL = `
INSERT INTO charging_session_events (
  session_id, event_type, occurred_at, ingested_at, energy_register_wh, stop_reason, source_protocol
) VALUES ($1, $2, $3, $4, $5, $6, $7)
ON CONFLICT (session_id, event_type, occurred_at) DO NOTHING`

// upsertSessionSQL folds one event into the session projection.
//
// Every column is merged rather than overwritten, because events legitimately
// arrive out of order: OCPP allows a StopTransaction to reach the server before
// the StartTransaction it closes, and a charge point that buffered during an
// outage replays whatever it held. First-write-wins on the boundary columns and
// GREATEST on the monotonic meter register make redelivery harmless.
const upsertSessionSQL = `
INSERT INTO charging_sessions (
  session_id, station_id, connector_id, site_id, source_protocol, transaction_reference,
  token_type, token_hash, authorization_reference,
  started_at, stopped_at, meter_start_wh, meter_last_wh, meter_stop_wh, stop_reason, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, now())
ON CONFLICT (session_id) DO UPDATE SET
  station_id = COALESCE(NULLIF(EXCLUDED.station_id, ''), charging_sessions.station_id),
  connector_id = COALESCE(NULLIF(EXCLUDED.connector_id, ''), charging_sessions.connector_id),
  site_id = COALESCE(NULLIF(EXCLUDED.site_id, ''), charging_sessions.site_id),
  source_protocol = COALESCE(NULLIF(EXCLUDED.source_protocol, ''), charging_sessions.source_protocol),
  transaction_reference = COALESCE(NULLIF(EXCLUDED.transaction_reference, ''), charging_sessions.transaction_reference),
  token_type = CASE WHEN EXCLUDED.token_type <> 'UNSPECIFIED' THEN EXCLUDED.token_type ELSE charging_sessions.token_type END,
  token_hash = COALESCE(NULLIF(EXCLUDED.token_hash, ''), charging_sessions.token_hash),
  authorization_reference = COALESCE(NULLIF(EXCLUDED.authorization_reference, ''), charging_sessions.authorization_reference),
  started_at = COALESCE(charging_sessions.started_at, EXCLUDED.started_at),
  stopped_at = COALESCE(charging_sessions.stopped_at, EXCLUDED.stopped_at),
  meter_start_wh = COALESCE(charging_sessions.meter_start_wh, EXCLUDED.meter_start_wh),
  meter_last_wh = GREATEST(charging_sessions.meter_last_wh, EXCLUDED.meter_last_wh),
  meter_stop_wh = COALESCE(charging_sessions.meter_stop_wh, EXCLUDED.meter_stop_wh),
  stop_reason = COALESCE(NULLIF(EXCLUDED.stop_reason, ''), charging_sessions.stop_reason),
  updated_at = now()`

// sessionColumns derives delivered energy from the register readings. It stays
// NULL while the opening reading is unknown: reporting zero would be
// indistinguishable from a session that genuinely delivered nothing.
const sessionColumns = `
  session_id, station_id, connector_id, site_id, source_protocol, transaction_reference,
  token_type, token_hash, authorization_reference, started_at, stopped_at,
  meter_start_wh, meter_stop_wh,
  CASE
    WHEN meter_start_wh IS NOT NULL AND COALESCE(meter_stop_wh, meter_last_wh) IS NOT NULL
    THEN GREATEST(COALESCE(meter_stop_wh, meter_last_wh) - meter_start_wh, 0)
    ELSE NULL
  END AS energy_delivered_wh,
  stop_reason, (stopped_at IS NULL) AS open, updated_at`

func (postgres *Postgres) PersistSessionEventBatch(ctx context.Context, events []domain.SessionEvent) error {
	if len(events) == 0 {
		return nil
	}
	return postgres.inTransaction(ctx, func(tx pgx.Tx) error {
		batch := &pgx.Batch{}
		for _, event := range events {
			batch.Queue(insertSessionEventSQL,
				event.SessionID, event.Type, event.OccurredAt, event.IngestedAt,
				event.EnergyRegisterWh, event.StopReason, event.SourceProtocol)

			startedAt, stoppedAt := sessionBoundaries(event)
			meterStart, meterLast, meterStop := sessionMeters(event)
			batch.Queue(upsertSessionSQL,
				event.SessionID, event.StationID, event.ConnectorID, event.SiteID,
				event.SourceProtocol, event.TransactionReference,
				event.TokenType, event.TokenHash, event.AuthorizationRef,
				startedAt, stoppedAt, meterStart, meterLast, meterStop, event.StopReason)
		}
		return execBatch(ctx, tx, batch, "session event")
	})
}

// sessionBoundaries maps an event onto the two columns it is allowed to open or
// close, so that an UPDATED event can never accidentally start or end a session.
func sessionBoundaries(event domain.SessionEvent) (startedAt, stoppedAt *time.Time) {
	occurred := event.OccurredAt
	switch event.Type {
	case domain.SessionStarted:
		return &occurred, nil
	case domain.SessionStopped:
		return nil, &occurred
	default:
		return nil, nil
	}
}

func sessionMeters(event domain.SessionEvent) (start, last, stop *int64) {
	switch event.Type {
	case domain.SessionStarted:
		return event.EnergyRegisterWh, event.EnergyRegisterWh, nil
	case domain.SessionStopped:
		return nil, event.EnergyRegisterWh, event.EnergyRegisterWh
	default:
		return nil, event.EnergyRegisterWh, nil
	}
}

func (postgres *Postgres) GetSession(ctx context.Context, sessionID string) (domain.Session, bool, error) {
	row := postgres.pool.QueryRow(ctx,
		`SELECT `+sessionColumns+` FROM charging_sessions WHERE session_id = $1`, sessionID)
	session, err := scanSession(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Session{}, false, nil
	}
	if err != nil {
		return domain.Session{}, false, fmt.Errorf("get session: %w", err)
	}
	return session, true, nil
}

func (postgres *Postgres) ListSessions(ctx context.Context, query domain.SessionQuery) ([]domain.Session, error) {
	page := query.Page.Normalize()
	rows, err := postgres.pool.Query(ctx, `
SELECT `+sessionColumns+`
FROM charging_sessions
WHERE session_id > $1
  AND ($2 = '' OR station_id = $2)
  AND (NOT $3 OR stopped_at IS NULL)
ORDER BY session_id
LIMIT $4`, page.After, query.StationID, query.OpenOnly, page.Limit)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	sessions := make([]domain.Session, 0, page.Limit)
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return sessions, nil
}

type scanner interface {
	Scan(destinations ...any) error
}

func scanSession(row scanner) (domain.Session, error) {
	var session domain.Session
	var startedAt, stoppedAt pgtype.Timestamptz
	var meterStart, meterStop, delivered pgtype.Int8

	err := row.Scan(
		&session.SessionID, &session.StationID, &session.ConnectorID, &session.SiteID,
		&session.SourceProtocol, &session.TransactionReference,
		&session.TokenType, &session.TokenHash, &session.AuthorizationRef,
		&startedAt, &stoppedAt, &meterStart, &meterStop, &delivered,
		&session.StopReason, &session.Open, &session.UpdatedAt,
	)
	if err != nil {
		return domain.Session{}, err
	}
	session.StartedAt = optionalTime(startedAt)
	session.StoppedAt = optionalTime(stoppedAt)
	session.MeterStartWh = optionalInt(meterStart)
	session.MeterStopWh = optionalInt(meterStop)
	session.EnergyDeliveredWh = optionalInt(delivered)
	return session, nil
}

func optionalInt(value pgtype.Int8) *int64 {
	if !value.Valid {
		return nil
	}
	result := value.Int64
	return &result
}

func optionalTime(value pgtype.Timestamptz) *time.Time {
	if !value.Valid {
		return nil
	}
	result := value.Time.UTC()
	return &result
}
