// Command ocpp-gateway is an OCPP 1.6J central system that speaks ORVOLT.
//
// It exists because ORVOLT's own telemetry format is not a thing any commercial
// charger emits. A charger speaks OCPP over a WebSocket; this service accepts
// that, translates it into the canonical contracts, and publishes them. Nothing
// downstream needs to know OCPP exists.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	ocpp16 "github.com/lorenzodonini/ocpp-go/ocpp1.6"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/orvolt/orvolt/cloud/ocpp-gateway/internal/config"
	"github.com/orvolt/orvolt/cloud/ocpp-gateway/internal/csms"
	"github.com/orvolt/orvolt/cloud/ocpp-gateway/internal/identity"
	"github.com/orvolt/orvolt/cloud/ocpp-gateway/internal/publisher"
	"github.com/orvolt/orvolt/cloud/shared/bus"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("ocpp gateway exited with an error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	settings := config.Load()

	// Fail before accepting a single charge point rather than after writing
	// reversible card identifiers into permanent billing records.
	hasher, err := identity.NewHasher(settings.TokenPepper)
	if err != nil {
		return fmt.Errorf("TOKEN_PEPPER: %w", err)
	}
	if settings.AuthorizationMode == csms.AllowAll {
		slog.Warn("authorization is set to allow-all: every presented token will be accepted",
			"guidance", "acceptable only on an isolated network with supervised hardware")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	connection, err := bus.Connect(ctx, bus.Options{
		URL:             settings.NATSURL,
		CredentialsFile: settings.NATSCredentials,
		CAFile:          settings.NATSCAFile,
		Name:            "orvolt-ocpp-gateway",
	})
	if err != nil {
		return err
	}
	defer func() { _ = connection.Drain() }()

	stream, err := jetstream.New(connection)
	if err != nil {
		return err
	}

	// Publish under this gateway's own identity so the control plane can verify
	// the origin of what it ingests.
	telemetrySubject, err := bus.DeviceSubject(settings.TelemetrySubject, settings.GatewayID)
	if err != nil {
		return fmt.Errorf("GATEWAY_ID cannot address a subject: %w", err)
	}
	sessionSubject, err := bus.DeviceSubject(settings.SessionSubject, settings.GatewayID)
	if err != nil {
		return fmt.Errorf("GATEWAY_ID cannot address a subject: %w", err)
	}

	// The control plane owns stream configuration, so the gateway waits for the
	// streams instead of creating them with a second, possibly conflicting,
	// retention policy.
	if err := waitForSubjects(ctx, stream, telemetrySubject, sessionSubject); err != nil {
		return err
	}

	handler := csms.NewHandler(
		csms.Options{
			Origin: csms.Origin{
				GatewayID: settings.GatewayID,
				SiteID:    settings.SiteID,
			},
			AuthorizationMode: settings.AuthorizationMode,
			HeartbeatInterval: settings.HeartbeatInterval,
			PublishTimeout:    settings.PublishTimeout,
		},
		publisher.New(stream, telemetrySubject, sessionSubject),
		hasher,
		csms.NewMonotonicIDs(),
	)

	centralSystem := ocpp16.NewCentralSystem(nil, nil)
	centralSystem.SetCoreHandler(handler)
	centralSystem.SetNewChargePointHandler(func(chargePoint ocpp16.ChargePointConnection) {
		slog.Info("charge point connected", "charge_point_id", chargePoint.ID())
	})
	centralSystem.SetChargePointDisconnectedHandler(func(chargePoint ocpp16.ChargePointConnection) {
		// A disconnect is not an outage by itself: charge points reconnect
		// constantly. It becomes one when the station stops reporting, which
		// the control plane's silence detection is what actually catches.
		slog.Warn("charge point disconnected", "charge_point_id", chargePoint.ID())
	})

	health := &http.Server{
		Addr:              settings.HTTPAddr,
		Handler:           healthHandler(connection.IsConnected),
		ReadHeaderTimeout: 5 * time.Second,
	}
	go func() {
		if err := health.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("health endpoint failed", "error", err)
		}
	}()

	go func() {
		<-ctx.Done()
		slog.Info("shutdown requested; closing charge point connections")
		centralSystem.Stop()
		shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = health.Shutdown(shutdownContext)
	}()

	slog.Info("ocpp gateway listening",
		"port", settings.ListenPort,
		"path", settings.ListenPath,
		"authorization_mode", string(settings.AuthorizationMode),
	)
	// Start blocks until Stop is called.
	centralSystem.Start(settings.ListenPort, settings.ListenPath)
	slog.Info("ocpp gateway stopped")
	return nil
}

// waitForSubjects blocks until every subject the gateway publishes to is backed
// by a stream. Publishing before then would be refused, and a charge point
// would see its transaction rejected during an ordinary cold start.
func waitForSubjects(ctx context.Context, stream jetstream.JetStream, subjects ...string) error {
	for _, subject := range subjects {
		for attempt := 1; ; attempt++ {
			_, err := stream.StreamNameBySubject(ctx, subject)
			if err == nil {
				break
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			slog.Warn("waiting for the control plane to create its streams",
				"subject", subject, "attempt", attempt, "error", err)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(3 * time.Second):
			}
		}
	}
	return nil
}

func healthHandler(connected func() bool) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"status":"ok"}`))
	})
	mux.HandleFunc("GET /ready", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		if !connected() {
			writer.WriteHeader(http.StatusServiceUnavailable)
			_, _ = writer.Write([]byte(`{"status":"not_ready","reason":"event bus is unreachable"}`))
			return
		}
		_, _ = writer.Write([]byte(`{"status":"ready"}`))
	})
	return mux
}
