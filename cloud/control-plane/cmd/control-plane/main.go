package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/config"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/httpapi"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/ingest"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/metrics"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/store"
	"github.com/orvolt/orvolt/cloud/shared/bus"
)

// deadLetterRetention is deliberately shorter than the live streams. The
// dead-letter stream is an investigation buffer, not an archive.
const deadLetterRetention = 7 * 24 * time.Hour

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	if err := run(); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("control plane exited with an error", "error", err)
		os.Exit(1)
	}
}

func run() error {
	settings := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := store.Open(ctx, settings.PostgresDSN)
	if err != nil {
		return err
	}
	defer database.Close()
	if err := store.Migrate(ctx, database, settings.MigrationsDir); err != nil {
		return err
	}

	connection, err := bus.Connect(ctx, bus.Options{
		URL:             settings.NATSURL,
		CredentialsFile: settings.NATSCredentials,
		CAFile:          settings.NATSCAFile,
		Name:            "orvolt-control-plane",
	})
	if err != nil {
		return err
	}
	defer func() { _ = connection.Drain() }()

	stream, err := jetstream.New(connection)
	if err != nil {
		return err
	}

	deadLetter, err := ingest.NewDeadLetter(ctx, stream, settings.DLQSubject, deadLetterRetention)
	if err != nil {
		return err
	}

	telemetry, err := ingest.NewTelemetryRunner(ctx, stream, database, settings.TelemetrySubject,
		deadLetter, settings.StreamMaxAge, settings.StreamMaxBytes, settings.BatchSize, settings.BatchInterval, settings.RequireDeviceIdentity)
	if err != nil {
		return err
	}
	energy, err := ingest.NewEnergyRunner(ctx, stream, database, settings.EnergySubject,
		deadLetter, settings.StreamMaxAge, settings.StreamMaxBytes, settings.BatchSize, settings.BatchInterval)
	if err != nil {
		return err
	}
	// Sessions are billing evidence, so they are retained for far longer than
	// telemetry and never share the high-volume stream's eviction policy.
	sessions, err := ingest.NewSessionRunner(ctx, stream, database, settings.SessionSubject,
		deadLetter, settings.SessionRetention, settings.StreamMaxBytes, settings.BatchSize, settings.BatchInterval)
	if err != nil {
		return err
	}

	api := httpapi.New(database, readiness(database, connection), settings.StationSilenceAfter)
	server := &http.Server{
		Addr:              settings.HTTPAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	var waitGroup sync.WaitGroup
	for _, service := range []ingest.Service{telemetry, energy, sessions} {
		waitGroup.Add(1)
		go func(service ingest.Service) {
			defer waitGroup.Done()
			if err := service.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("ingest runner failed", "stream", service.Name(), "error", err)
				stop()
			}
		}(service)
	}

	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		watchSilentStations(ctx, database, settings.StationSilenceAfter)
	}()

	waitGroup.Add(1)
	go func() {
		defer waitGroup.Done()
		slog.Info("control plane listening", "address", settings.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			slog.Error("HTTP server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	slog.Info("shutdown requested; draining")

	shutdownContext, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownContext); err != nil {
		slog.Error("HTTP shutdown failed", "error", err)
	}
	waitGroup.Wait()
	slog.Info("control plane stopped")
	return nil
}

func readiness(database *store.Postgres, connection *nats.Conn) func(context.Context) error {
	return func(ctx context.Context) error {
		if err := database.Ping(ctx); err != nil {
			return errors.New("PostgreSQL is unreachable")
		}
		if connection.Status() != nats.CONNECTED {
			return errors.New("NATS is not connected")
		}
		return nil
	}
}

// watchSilentStations keeps the silent-station gauge current without waiting
// for somebody to call the API. Alerting on chargers that went dark only works
// if the number is published continuously.
func watchSilentStations(ctx context.Context, database *store.Postgres, threshold time.Duration) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			silent, err := database.ListSilentStations(ctx, threshold)
			if err != nil {
				slog.Warn("silent-station scan failed", "error", err)
				continue
			}
			metrics.SilentStations.Set(float64(len(silent)))
			if len(silent) > 0 {
				slog.Warn("stations are silent", "count", len(silent), "threshold", threshold.String())
			}
		}
	}
}
