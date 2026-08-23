package config

import (
	"log/slog"
	"os"
	"strconv"
	"time"
)

type Config struct {
	PostgresDSN   string
	MigrationsDir string
	HTTPAddr      string

	NATSURL          string
	NATSCredentials  string
	NATSCAFile       string
	TelemetrySubject string
	EnergySubject    string
	SessionSubject   string
	DLQSubject       string

	// JetStream retention. A stream without limits eventually fills the
	// broker's disk and stops accepting publishes, which stops the fleet.
	StreamMaxAge   time.Duration
	StreamMaxBytes int64

	// Sessions are billing evidence and outlive telemetry by design.
	SessionRetention time.Duration

	// RequireDeviceIdentity rejects telemetry that did not arrive on an
	// authenticated per-device subject. Off for local development, mandatory
	// for any bus an untrusted network can reach.
	RequireDeviceIdentity bool

	// Ingest batching. One database transaction per telemetry sample does not
	// survive fleet scale, so messages are grouped into a bounded window.
	BatchSize     int
	BatchInterval time.Duration

	// A station that has not been ingested within this window is reported as
	// silent. Losing sight of a charger is the primary operational signal in a
	// charging network.
	StationSilenceAfter time.Duration

	// Default site load-management policy. A real deployment resolves these per
	// site from a station registry; these are the fleet-wide defaults.
	SiteCapacityKW       float64
	SiteSafetyMarginKW   float64
	SiteFallbackKW       float64
	ObservationFreshness time.Duration
	MaxConnectorKW       float64

	// Advisory capacity proposals. Published for site-local policy to consume;
	// never applied to equipment by ORVOLT itself.
	AdviceSubject  string
	AdviceInterval time.Duration
}

func Load() Config {
	return Config{
		PostgresDSN:   env("POSTGRES_DSN", "postgres://orvolt:orvolt_dev_only@localhost:5432/orvolt?sslmode=disable"),
		MigrationsDir: env("MIGRATIONS_DIR", "cloud/control-plane/migrations"),
		HTTPAddr:      env("HTTP_ADDR", ":8080"),

		NATSURL:          env("NATS_URL", "nats://localhost:4222"),
		NATSCredentials:  env("NATS_CREDENTIALS", ""),
		NATSCAFile:       env("NATS_CA_FILE", ""),
		TelemetrySubject: env("NATS_SUBJECT", "orvolt.telemetry.evse.v1"),
		EnergySubject:    env("ENERGY_NATS_SUBJECT", "orvolt.energy.site.v1"),
		SessionSubject:   env("SESSION_NATS_SUBJECT", "orvolt.session.evse.v1"),
		DLQSubject:       env("DLQ_SUBJECT_PREFIX", "orvolt.dlq"),

		StreamMaxAge:   envDuration("STREAM_MAX_AGE", 720*time.Hour),
		StreamMaxBytes: envBytes("STREAM_MAX_BYTES", 8<<30),

		SessionRetention: envDuration("SESSION_STREAM_MAX_AGE", 8760*time.Hour),

		RequireDeviceIdentity: os.Getenv("REQUIRE_DEVICE_IDENTITY") == "true",

		BatchSize:     envInt("INGEST_BATCH_SIZE", 256),
		BatchInterval: envDuration("INGEST_BATCH_INTERVAL", 250*time.Millisecond),

		StationSilenceAfter: envDuration("STATION_SILENCE_AFTER", 5*time.Minute),

		SiteCapacityKW:       envFloat("SITE_CAPACITY_KW", 100),
		SiteSafetyMarginKW:   envFloat("SITE_SAFETY_MARGIN_KW", 5),
		SiteFallbackKW:       envFloat("SITE_FALLBACK_KW", 20),
		ObservationFreshness: envDuration("ENERGY_OBSERVATION_FRESHNESS", 5*time.Minute),
		MaxConnectorKW:       envFloat("MAX_CONNECTOR_KW", 350),

		AdviceSubject:  env("ADVICE_NATS_SUBJECT", "orvolt.control.advice.v1"),
		AdviceInterval: envDuration("ADVICE_INTERVAL", 30*time.Second),
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

func envInt(name string, fallback int) int {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		slog.Warn("ignoring invalid integer configuration", "name", name, "value", raw, "fallback", fallback)
		return fallback
	}
	return value
}

func envBytes(name string, fallback int64) int64 {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		slog.Warn("ignoring invalid byte-size configuration", "name", name, "value", raw, "fallback", fallback)
		return fallback
	}
	return value
}

func envDuration(name string, fallback time.Duration) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := time.ParseDuration(raw)
	if err != nil || value <= 0 {
		slog.Warn("ignoring invalid duration configuration", "name", name, "value", raw, "fallback", fallback)
		return fallback
	}
	return value
}

func envFloat(name string, fallback float64) float64 {
	raw := os.Getenv(name)
	if raw == "" {
		return fallback
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || value <= 0 {
		slog.Warn("ignoring invalid numeric configuration", "name", name, "value", raw, "fallback", fallback)
		return fallback
	}
	return value
}
