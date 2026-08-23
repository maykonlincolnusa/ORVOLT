package httpapi_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/httpapi"
)

type repository struct {
	stations  []domain.Station
	telemetry domain.Telemetry
	energy    domain.EnergyObservation
	silent    []domain.StationHealth
	lastPage  domain.Page
}

func (*repository) PersistTelemetryBatch(context.Context, []domain.Telemetry) error { return nil }
func (*repository) PersistEnergyObservationBatch(context.Context, []domain.EnergyObservation) error {
	return nil
}

func (repo *repository) ListStations(_ context.Context, page domain.Page) ([]domain.Station, error) {
	repo.lastPage = page
	if page.Limit < len(repo.stations) {
		return repo.stations[:page.Limit], nil
	}
	return repo.stations, nil
}

func (repo *repository) GetStation(_ context.Context, stationID string) (domain.Station, bool, error) {
	for _, station := range repo.stations {
		if station.StationID == stationID {
			return station, true, nil
		}
	}
	return domain.Station{}, false, nil
}

func (repo *repository) LatestTelemetry(_ context.Context, stationID string) (domain.Telemetry, bool, error) {
	return repo.telemetry, repo.telemetry.StationID == stationID, nil
}

func (repo *repository) LatestEnergyObservation(_ context.Context, siteID string) (domain.EnergyObservation, bool, error) {
	return repo.energy, repo.energy.SiteID == siteID, nil
}

func (repo *repository) ListSilentStations(context.Context, time.Duration) ([]domain.StationHealth, error) {
	return repo.silent, nil
}

func newAPI(repo *repository) http.Handler {
	return httpapi.New(repo, func(context.Context) error { return nil }, 5*time.Minute).Handler()
}

func sampleRepository() *repository {
	return &repository{
		stations:  []domain.Station{{StationID: "station-1", LastSeenAt: time.Unix(1, 0)}},
		telemetry: domain.Telemetry{StationID: "station-1", ConnectorID: "1", Timestamp: time.Unix(1, 0)},
		energy:    domain.EnergyObservation{SiteID: "site-1", Provider: "SMA", ObservedAt: time.Unix(1, 0)},
	}
}

func TestRoutesAnswer(t *testing.T) {
	api := newAPI(sampleRepository())

	paths := []string{
		"/health",
		"/ready",
		"/metrics",
		"/api/v1/stations",
		"/api/v1/stations/station-1",
		"/api/v1/stations/station-1/telemetry/latest",
		"/api/v1/energy/sites/site-1/latest",
		"/api/v1/fleet/silent-stations",
	}
	for _, path := range paths {
		response := httptest.NewRecorder()
		api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Errorf("%s returned %d: %s", path, response.Code, response.Body.String())
		}
	}
}

func TestUnknownStationIsNotFound(t *testing.T) {
	api := newAPI(sampleRepository())
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/stations/nope", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown station, got %d", response.Code)
	}
}

// Listing the fleet must always be bounded; the previous version serialised
// every station in one response.
func TestStationListingIsBounded(t *testing.T) {
	repo := sampleRepository()
	for index := 2; index <= 250; index++ {
		repo.stations = append(repo.stations, domain.Station{
			StationID:  "station-" + strconv.Itoa(index),
			LastSeenAt: time.Unix(1, 0),
		})
	}
	api := newAPI(repo)

	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/stations", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", response.Code)
	}

	var page struct {
		Stations []domain.Station `json:"stations"`
		Next     string           `json:"next"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &page); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(page.Stations) != domain.DefaultPageLimit {
		t.Fatalf("expected the default page limit of %d, got %d", domain.DefaultPageLimit, len(page.Stations))
	}
	if page.Next == "" {
		t.Error("a truncated listing must expose a cursor to continue from")
	}
}

func TestStationListingRejectsInvalidLimit(t *testing.T) {
	api := newAPI(sampleRepository())
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/stations?limit=-3", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for a negative limit, got %d", response.Code)
	}
}

func TestStationListingClampsOversizedLimit(t *testing.T) {
	repo := sampleRepository()
	api := newAPI(repo)
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/stations?limit=100000", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", response.Code)
	}
	if repo.lastPage.Limit != domain.MaxPageLimit {
		t.Fatalf("expected the limit to be clamped to %d, got %d", domain.MaxPageLimit, repo.lastPage.Limit)
	}
}

func TestSilentStationsReportsThreshold(t *testing.T) {
	repo := sampleRepository()
	repo.silent = []domain.StationHealth{{
		StationID:  "station-1",
		LastSeenAt: time.Unix(1, 0),
		SilentForS: 900,
	}}
	api := newAPI(repo)

	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/fleet/silent-stations?threshold=10m", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", response.Code)
	}

	var payload struct {
		ThresholdSeconds float64                `json:"threshold_seconds"`
		Stations         []domain.StationHealth `json:"stations"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if payload.ThresholdSeconds != 600 {
		t.Errorf("expected the requested threshold to be echoed, got %v", payload.ThresholdSeconds)
	}
	if len(payload.Stations) != 1 {
		t.Fatalf("expected one silent station, got %d", len(payload.Stations))
	}
}

func TestSilentStationsRejectsInvalidThreshold(t *testing.T) {
	api := newAPI(sampleRepository())
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/fleet/silent-stations?threshold=soon", nil))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for an invalid threshold, got %d", response.Code)
	}
}
