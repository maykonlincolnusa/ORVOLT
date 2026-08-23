package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
)

type API struct {
	repository domain.Repository
	ready      func(context.Context) error
}

func New(repository domain.Repository, ready func(context.Context) error) *API {
	return &API{repository: repository, ready: ready}
}

func (api *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", api.health)
	mux.HandleFunc("GET /ready", api.readiness)
	mux.HandleFunc("GET /api/v1/stations", api.listStations)
	mux.HandleFunc("GET /api/v1/stations/", api.stationRoutes)
	mux.HandleFunc("GET /api/v1/energy/sites/", api.energySiteRoutes)
	return mux
}

func (api *API) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (api *API) readiness(writer http.ResponseWriter, request *http.Request) {
	if err := api.ready(request.Context()); err != nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"status": "not_ready"})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
}

func (api *API) listStations(writer http.ResponseWriter, request *http.Request) {
	stations, err := api.repository.ListStations(request.Context())
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list stations"})
		return
	}
	writeJSON(writer, http.StatusOK, stations)
}

func (api *API) stationRoutes(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/api/v1/stations/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) == 0 || parts[0] == "" {
		http.NotFound(writer, request)
		return
	}
	stationID := parts[0]
	if len(parts) == 1 {
		api.getStation(writer, request, stationID)
		return
	}
	if len(parts) == 3 && parts[1] == "telemetry" && parts[2] == "latest" {
		api.latestTelemetry(writer, request, stationID)
		return
	}
	http.NotFound(writer, request)
}

func (api *API) getStation(writer http.ResponseWriter, request *http.Request, stationID string) {
	station, found, err := api.repository.GetStation(request.Context(), stationID)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to get station"})
		return
	}
	if !found {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": "station not found"})
		return
	}
	writeJSON(writer, http.StatusOK, station)
}

func (api *API) latestTelemetry(writer http.ResponseWriter, request *http.Request, stationID string) {
	telemetry, found, err := api.repository.LatestTelemetry(request.Context(), stationID)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to get telemetry"})
		return
	}
	if !found {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": "telemetry not found"})
		return
	}
	writeJSON(writer, http.StatusOK, telemetry)
}

func (api *API) energySiteRoutes(writer http.ResponseWriter, request *http.Request) {
	path := strings.TrimPrefix(request.URL.Path, "/api/v1/energy/sites/")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] != "latest" {
		http.NotFound(writer, request)
		return
	}
	observation, found, err := api.repository.LatestEnergyObservation(request.Context(), parts[0])
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to get energy observation"})
		return
	}
	if !found {
		writeJSON(writer, http.StatusNotFound, map[string]string{"error": "energy observation not found"})
		return
	}
	writeJSON(writer, http.StatusOK, observation)
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
