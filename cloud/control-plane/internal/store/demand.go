package store

import (
	"context"
	"fmt"
	"time"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
)

// chargingStates are the connector states that consume power or are about to.
// Load management must reserve capacity for a connector that is preparing, or
// the vehicle draws current the site never budgeted for.
var chargingStates = []string{"CHARGING", "PREPARING", "SUSPENDED", "FINISHING"}

// ListSiteDemand returns the latest reading for each connector at a site that
// is currently drawing, or about to draw, power.
//
// DISTINCT ON collapses the append-only telemetry history to one row per
// connector, ordered by the control plane's own arrival stamp so a station with
// a wrong clock cannot present an old reading as current.
func (postgres *Postgres) ListSiteDemand(ctx context.Context, siteID string, within time.Duration) ([]domain.ConnectorDemand, error) {
	rows, err := postgres.pool.Query(ctx, `
SELECT DISTINCT ON (station_id, connector_id)
       station_id, connector_id, power_kw, state, ingested_at
FROM telemetry
WHERE site_id = $1
  AND ingested_at > now() - $2::interval
  AND state = ANY($3)
ORDER BY station_id, connector_id, ingested_at DESC, id DESC`, siteID, within, chargingStates)
	if err != nil {
		return nil, fmt.Errorf("list site demand: %w", err)
	}
	defer rows.Close()

	demand := make([]domain.ConnectorDemand, 0)
	for rows.Next() {
		var connector domain.ConnectorDemand
		if err := rows.Scan(&connector.StationID, &connector.ConnectorID,
			&connector.PowerKW, &connector.State, &connector.ObservedAt); err != nil {
			return nil, fmt.Errorf("scan site demand: %w", err)
		}
		demand = append(demand, connector)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate site demand: %w", err)
	}
	return demand, nil
}

// ListActiveSites returns sites that reported recently, so that advice is
// computed only for sites doing something.
func (postgres *Postgres) ListActiveSites(ctx context.Context, within time.Duration) ([]string, error) {
	rows, err := postgres.pool.Query(ctx, `
SELECT DISTINCT site_id
FROM telemetry
WHERE ingested_at > now() - $1::interval
ORDER BY site_id`, within)
	if err != nil {
		return nil, fmt.Errorf("list active sites: %w", err)
	}
	defer rows.Close()

	sites := make([]string, 0)
	for rows.Next() {
		var site string
		if err := rows.Scan(&site); err != nil {
			return nil, fmt.Errorf("scan active site: %w", err)
		}
		sites = append(sites, site)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate active sites: %w", err)
	}
	return sites, nil
}
