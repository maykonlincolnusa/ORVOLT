package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/metrics"
)

type API struct {
	repository     domain.Repository
	ready          func(context.Context) error
	silenceAfter   time.Duration
	maxRequestTime time.Duration
}

func New(repository domain.Repository, ready func(context.Context) error, silenceAfter time.Duration) *API {
	return &API{
		repository:     repository,
		ready:          ready,
		silenceAfter:   silenceAfter,
		maxRequestTime: 15 * time.Second,
	}
}

func (api *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /metrics", metrics.Handler())
	api.route(mux, "GET /health", "health", api.health)
	api.route(mux, "GET /ready", "ready", api.readiness)
	api.route(mux, "GET /api/v1/stations", "stations.list", api.listStations)
	api.route(mux, "GET /api/v1/stations/{stationID}", "stations.get", api.getStation)
	api.route(mux, "GET /api/v1/stations/{stationID}/telemetry/latest", "telemetry.latest", api.latestTelemetry)
	api.route(mux, "GET /api/v1/energy/sites/{siteID}/latest", "energy.latest", api.latestEnergy)
	api.route(mux, "GET /api/v1/fleet/silent-stations", "fleet.silent", api.silentStations)
	return mux
}

// route registers a handler and records its outcome. Instrumenting at
// registration keeps every route counted without a per-handler reminder.
func (api *API) route(mux *http.ServeMux, pattern, name string, handler http.HandlerFunc) {
	mux.Handle(pattern, http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		ctx, cancel := context.WithTimeout(request.Context(), api.maxRequestTime)
		defer cancel()
		recorder := &statusRecorder{ResponseWriter: writer, status: http.StatusOK}
		handler(recorder, request.WithContext(ctx))
		metrics.HTTPRequests.WithLabelValues(name, statusClass(recorder.status)).Inc()
	}))
}

func (api *API) health(writer http.ResponseWriter, _ *http.Request) {
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ok"})
}

func (api *API) readiness(writer http.ResponseWriter, request *http.Request) {
	if err := api.ready(request.Context()); err != nil {
		writeJSON(writer, http.StatusServiceUnavailable, map[string]string{"status": "not_ready", "reason": err.Error()})
		return
	}
	writeJSON(writer, http.StatusOK, map[string]string{"status": "ready"})
}

// stationPage is a bounded listing. The unbounded version returned the entire
// fleet in a single response, which stops working at the scale this system is
// designed for.
type stationPage struct {
	Stations []domain.Station `json:"stations"`
	Next     string           `json:"next,omitempty"`
}

func (api *API) listStations(writer http.ResponseWriter, request *http.Request) {
	page := domain.Page{After: request.URL.Query().Get("after")}
	if raw := request.URL.Query().Get("limit"); raw != "" {
		limit, err := strconv.Atoi(raw)
		if err != nil || limit <= 0 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "limit must be a positive integer"})
			return
		}
		page.Limit = limit
	}
	page = page.Normalize()

	stations, err := api.repository.ListStations(request.Context(), page)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list stations"})
		return
	}
	response := stationPage{Stations: stations}
	if len(stations) == page.Limit {
		response.Next = stations[len(stations)-1].StationID
	}
	writeJSON(writer, http.StatusOK, response)
}

func (api *API) getStation(writer http.ResponseWriter, request *http.Request) {
	station, found, err := api.repository.GetStation(request.Context(), request.PathValue("stationID"))
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

func (api *API) latestTelemetry(writer http.ResponseWriter, request *http.Request) {
	telemetry, found, err := api.repository.LatestTelemetry(request.Context(), request.PathValue("stationID"))
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

func (api *API) latestEnergy(writer http.ResponseWriter, request *http.Request) {
	observation, found, err := api.repository.LatestEnergyObservation(request.Context(), request.PathValue("siteID"))
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

type silentStationsResponse struct {
	ThresholdSeconds float64                `json:"threshold_seconds"`
	Stations         []domain.StationHealth `json:"stations"`
}

func (api *API) silentStations(writer http.ResponseWriter, request *http.Request) {
	threshold := api.silenceAfter
	if raw := request.URL.Query().Get("threshold"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil || parsed <= 0 {
			writeJSON(writer, http.StatusBadRequest, map[string]string{"error": "threshold must be a positive Go duration, for example 5m"})
			return
		}
		threshold = parsed
	}
	stations, err := api.repository.ListSilentStations(request.Context(), threshold)
	if err != nil {
		writeJSON(writer, http.StatusInternalServerError, map[string]string{"error": "unable to list silent stations"})
		return
	}
	metrics.SilentStations.Set(float64(len(stations)))
	writeJSON(writer, http.StatusOK, silentStationsResponse{
		ThresholdSeconds: threshold.Seconds(),
		Stations:         stations,
	})
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (recorder *statusRecorder) WriteHeader(status int) {
	recorder.status = status
	recorder.ResponseWriter.WriteHeader(status)
}

func statusClass(status int) string {
	switch {
	case status >= 500:
		return "5xx"
	case status >= 400:
		return "4xx"
	case status >= 300:
		return "3xx"
	default:
		return "2xx"
	}
}

func writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
