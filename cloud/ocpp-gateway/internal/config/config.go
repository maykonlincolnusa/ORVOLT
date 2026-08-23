package config

import (
	"log/slog"
	"os"
	"strconv"
	"time"

	"github.com/orvolt/orvolt/cloud/ocpp-gateway/internal/csms"
)

type Config struct {
	ListenPort int
	ListenPath string
	HTTPAddr   string

	NATSURL          string
	NATSCredentials  string
	NATSCAFile       string
	TelemetrySubject string
	SessionSubject   string

	GatewayID string
	SiteID    string

	TokenPepper       string
	AuthorizationMode csms.AuthorizationMode
	HeartbeatInterval time.Duration
	PublishTimeout    time.Duration
}

func Load() Config {
	return Config{
		// 8887 is the conventional OCPP 1.6J port. The path carries the charge
		// point identity, which is how OCPP names a station on connect.
		ListenPort: envInt("OCPP_LISTEN_PORT", 8887),
		ListenPath: env("OCPP_LISTEN_PATH", "/ocpp/{ws}"),
		HTTPAddr:   env("HTTP_ADDR", ":8081"),

		NATSURL:          env("NATS_URL", "nats://localhost:4222"),
		NATSCredentials:  env("NATS_CREDENTIALS", ""),
		NATSCAFile:       env("NATS_CA_FILE", ""),
		TelemetrySubject: env("NATS_SUBJECT", "orvolt.telemetry.evse.v1"),
		SessionSubject:   env("SESSION_NATS_SUBJECT", "orvolt.session.evse.v1"),

		GatewayID: env("GATEWAY_ID", "ocpp-gateway-001"),
		SiteID:    env("SITE_ID", "site-dev-001"),

		TokenPepper:       os.Getenv("TOKEN_PEPPER"),
		AuthorizationMode: authorizationMode(),
		HeartbeatInterval: envDuration("HEARTBEAT_INTERVAL", 5*time.Minute),
		PublishTimeout:    envDuration("PUBLISH_TIMEOUT", 10*time.Second),
	}
}

// authorizationMode fails safe. Any value other than an explicit "allow-all"
// leaves the gateway refusing tokens, because approving a card the platform
// cannot verify would let anyone charge for free.
func authorizationMode() csms.AuthorizationMode {
	switch os.Getenv("AUTHORIZATION_MODE") {
	case string(csms.AllowAll):
		return csms.AllowAll
	case "", string(csms.DenyAll):
		return csms.DenyAll
	default:
		slog.Warn("unknown authorization mode; refusing all tokens",
			"value", os.Getenv("AUTHORIZATION_MODE"))
		return csms.DenyAll
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
		slog.Warn("ignoring invalid integer configuration", "name", name, "value", raw)
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
		slog.Warn("ignoring invalid duration configuration", "name", name, "value", raw)
		return fallback
	}
	return value
}
