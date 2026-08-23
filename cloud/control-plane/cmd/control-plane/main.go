package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/config"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/httpapi"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/ingest"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)
	config := config.Load()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	database, err := store.Open(ctx, config.PostgresDSN)
	if err != nil {
		logger.Error("database connection failed", "error", err)
		os.Exit(1)
	}
	defer database.Close()
	if err := store.Migrate(ctx, database, config.MigrationsDir); err != nil {
		logger.Error("database migration failed", "error", err)
		os.Exit(1)
	}

	nc, err := connectNATS(ctx, config.NATSURL)
	if err != nil {
		logger.Error("NATS connection failed", "error", err)
		os.Exit(1)
	}
	defer nc.Drain()

	consumer, err := ingest.NewConsumer(nc, database, config.NATSSubject)
	if err != nil {
		logger.Error("JetStream setup failed", "error", err)
		os.Exit(1)
	}
	if err := consumer.Start(ctx); err != nil {
		logger.Error("JetStream consumer failed to start", "error", err)
		os.Exit(1)
	}
	energyConsumer, err := ingest.NewEnergyConsumer(nc, database, config.EnergySubject)
	if err != nil {
		logger.Error("energy JetStream setup failed", "error", err)
		os.Exit(1)
	}
	if err := energyConsumer.Start(ctx); err != nil {
		logger.Error("energy JetStream consumer failed to start", "error", err)
		os.Exit(1)
	}

	api := httpapi.New(database, func(requestContext context.Context) error {
		if err := database.Ping(requestContext); err != nil {
			return err
		}
		if nc.Status() != nats.CONNECTED {
			return errors.New("NATS is not connected")
		}
		return nil
	})
	server := &http.Server{
		Addr:              config.HTTPAddr,
		Handler:           api.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("control plane listening", "address", config.HTTPAddr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("HTTP server failed", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownContext, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	consumer.Stop()
	energyConsumer.Stop()
	if err := server.Shutdown(shutdownContext); err != nil {
		logger.Error("HTTP shutdown failed", "error", err)
	}
}

func connectNATS(ctx context.Context, url string) (*nats.Conn, error) {
	for {
		connection, err := nats.Connect(url, nats.Name("orvolt-control-plane"), nats.MaxReconnects(-1))
		if err == nil {
			return connection, nil
		}
		slog.Warn("NATS unavailable; retrying", "error", err, "url", url)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(2 * time.Second):
		}
	}
}
