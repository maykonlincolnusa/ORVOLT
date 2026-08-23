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
	stations         []domain.Station
	telemetry        domain.Telemetry
	energy           domain.EnergyObservation
	silent           []domain.StationHealth
	sessions         []domain.Session
	lastPage         domain.Page
	lastSessionQuery domain.SessionQuery
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

func (*repository) PersistSessionEventBatch(context.Context, []domain.SessionEvent) error {
	return nil
}

func (repo *repository) GetSession(_ context.Context, sessionID string) (domain.Session, bool, error) {
	for _, session := range repo.sessions {
		if session.SessionID == sessionID {
			return session, true, nil
		}
	}
	return domain.Session{}, false, nil
}

func (repo *repository) ListSessions(_ context.Context, query domain.SessionQuery) ([]domain.Session, error) {
	repo.lastSessionQuery = query
	matching := make([]domain.Session, 0, len(repo.sessions))
	for _, session := range repo.sessions {
		if query.StationID != "" && session.StationID != query.StationID {
			continue
		}
		if query.OpenOnly && !session.Open {
			continue
		}
		matching = append(matching, session)
	}
	if query.Page.Limit < len(matching) {
		return matching[:query.Page.Limit], nil
	}
	return matching, nil
}

func newAPI(repo *repository) http.Handler {
	return httpapi.New(repo, func(context.Context) error { return nil }, 5*time.Minute).Handler()
}

func sampleRepository() *repository {
	return &repository{
		stations:  []domain.Station{{StationID: "station-1", LastSeenAt: time.Unix(1, 0)}},
		telemetry: domain.Telemetry{StationID: "station-1", ConnectorID: "1", Timestamp: time.Unix(1, 0)},
		energy:    domain.EnergyObservation{SiteID: "site-1", Provider: "SMA", ObservedAt: time.Unix(1, 0)},
		sessions: []domain.Session{
			{SessionID: "ocpp16:station-1:900", StationID: "station-1", Open: false},
			{SessionID: "ocpp16:station-1:901", StationID: "station-1", Open: true},
			{SessionID: "ocpp16:station-2:902", StationID: "station-2", Open: true},
		},
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
		"/api/v1/sessions",
		"/api/v1/sessions/ocpp16:station-1:900",
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

// Open sessions are the ones an operator chases: they hold a connector and are
// the usual root of a billing dispute.
func TestSessionListingFiltersOpenSessions(t *testing.T) {
	repo := sampleRepository()
	api := newAPI(repo)

	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions?open=true&station=station-1", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("unexpected status %d", response.Code)
	}

	var payload struct {
		Sessions []domain.Session `json:"sessions"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decoding response: %v", err)
	}
	if len(payload.Sessions) != 1 || payload.Sessions[0].SessionID != "ocpp16:station-1:901" {
		t.Fatalf("expected only the open session of station-1, got %+v", payload.Sessions)
	}
	if !repo.lastSessionQuery.OpenOnly || repo.lastSessionQuery.StationID != "station-1" {
		t.Errorf("filters were not passed through: %+v", repo.lastSessionQuery)
	}
}

func TestUnknownSessionIsNotFound(t *testing.T) {
	api := newAPI(sampleRepository())
	response := httptest.NewRecorder()
	api.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/does-not-exist", nil))
	if response.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for an unknown session, got %d", response.Code)
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
