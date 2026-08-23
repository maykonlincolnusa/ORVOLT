package httpapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/httpapi"
)

type repository struct {
	station     domain.Station
	telemetry   domain.Telemetry
	energy      domain.EnergyObservation
}

func (*repository) PersistTelemetry(context.Context, domain.Telemetry) error { return nil }
func (*repository) PersistEnergyObservation(context.Context, domain.EnergyObservation) error {
	return nil
}
func (repository *repository) ListStations(context.Context) ([]domain.Station, error) {
	return []domain.Station{repository.station}, nil
}
func (repository *repository) GetStation(_ context.Context, stationID string) (domain.Station, bool, error) {
	return repository.station, repository.station.StationID == stationID, nil
}
func (repository *repository) LatestTelemetry(_ context.Context, stationID string) (domain.Telemetry, bool, error) {
	return repository.telemetry, repository.telemetry.StationID == stationID, nil
}
func (repository *repository) LatestEnergyObservation(_ context.Context, siteID string) (domain.EnergyObservation, bool, error) {
	return repository.energy, repository.energy.SiteID == siteID, nil
}

func TestStationAndLatestTelemetryRoutes(t *testing.T) {
	repository := &repository{
		station:   domain.Station{StationID: "station-1", LastSeenAt: time.Unix(1, 0)},
		telemetry: domain.Telemetry{StationID: "station-1", ConnectorID: "1", Timestamp: time.Unix(1, 0)},
		energy:    domain.EnergyObservation{SiteID: "site-1", Provider: "SMA", ObservedAt: time.Unix(1, 0)},
	}
	api := httpapi.New(repository, func(context.Context) error { return nil }).Handler()

	for _, path := range []string{"/health", "/ready", "/api/v1/stations", "/api/v1/stations/station-1", "/api/v1/stations/station-1/telemetry/latest", "/api/v1/energy/sites/site-1/latest"} {
		response := httptest.NewRecorder()
		api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", path, response.Code, response.Body.String())
		}
	}
}
