// Package metrics holds the control plane's Prometheus instrumentation.
//
// Structured logs answer "what happened to this message". These counters answer
// "is the fleet healthy right now", which is the question an operator actually
// pages on.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

// Ingest outcomes recorded on the messages counter.
const (
	ResultPersisted    = "persisted"
	ResultDeadLettered = "dead_lettered"
	ResultRetried      = "retried"
)

var (
	registry = prometheus.NewRegistry()

	Messages = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "orvolt_ingest_messages_total",
		Help: "Messages consumed from JetStream by stream and outcome.",
	}, []string{"stream", "result"})

	BatchSize = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "orvolt_ingest_batch_size",
		Help:    "Number of messages persisted per database transaction.",
		Buckets: []float64{1, 2, 4, 8, 16, 32, 64, 128, 256, 512, 1024},
	}, []string{"stream"})

	PersistDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "orvolt_ingest_persist_duration_seconds",
		Help:    "Time spent persisting one batch.",
		Buckets: prometheus.DefBuckets,
	}, []string{"stream"})

	// UnsynchronizedClock counts observations whose device clock could not be
	// trusted. A rising rate means chargers are booting without NTP.
	UnsynchronizedClock = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "orvolt_telemetry_unsynchronized_clock_total",
		Help: "Telemetry accepted from a station whose clock is not synchronised.",
	})

	// SilentStations is the number of stations past the silence threshold.
	// A charger going dark is the primary operational signal in a network.
	SilentStations = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "orvolt_stations_silent",
		Help: "Stations that have not reported within the configured silence window.",
	})

	HTTPRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: "orvolt_http_requests_total",
		Help: "Management API requests by route and status class.",
	}, []string{"route", "status"})
)

func init() {
	registry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		Messages,
		BatchSize,
		PersistDuration,
		UnsynchronizedClock,
		SilentStations,
		HTTPRequests,
	)
}

// Handler serves the Prometheus exposition format.
func Handler() http.Handler {
	return promhttp.HandlerFor(registry, promhttp.HandlerOpts{})
}
