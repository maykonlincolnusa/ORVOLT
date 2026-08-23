package config

import "os"

type Config struct {
	PostgresDSN   string
	NATSURL       string
	NATSSubject   string
	EnergySubject string
	HTTPAddr      string
	MigrationsDir string
}

func Load() Config {
	return Config{
		PostgresDSN:   env("POSTGRES_DSN", "postgres://orvolt:orvolt_dev_only@localhost:5432/orvolt?sslmode=disable"),
		NATSURL:       env("NATS_URL", "nats://localhost:4222"),
		NATSSubject:   env("NATS_SUBJECT", "orvolt.telemetry.evse.v1"),
		EnergySubject: env("ENERGY_NATS_SUBJECT", "orvolt.energy.site.v1"),
		HTTPAddr:      env("HTTP_ADDR", ":8080"),
		MigrationsDir: env("MIGRATIONS_DIR", "cloud/control-plane/migrations"),
	}
}

func env(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}
