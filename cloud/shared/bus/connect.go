// Package bus holds the NATS connection policy shared by every cloud service.
//
// It exists so that credentials, TLS material and reconnect behaviour are
// configured identically in the control plane and in protocol gateways. A
// service that opens its own raw connection would silently opt out of
// authentication.
package bus

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
)

// Options describes how to reach the event bus.
type Options struct {
	URL string
	// CredentialsFile is a NATS .creds file holding the service's JWT and seed.
	// Empty means anonymous, which is acceptable only on an isolated
	// development network.
	CredentialsFile string
	// CAFile is the trust root used to verify the broker's TLS certificate.
	CAFile string
	// Name identifies this client in NATS monitoring output.
	Name string
}

// Connect dials NATS, retrying until it succeeds or the context is cancelled.
// A charging network's broker restarts; a service that exits on the first
// refused connection turns a broker restart into a fleet outage.
func Connect(ctx context.Context, options Options) (*nats.Conn, error) {
	dialOptions := []nats.Option{
		nats.Name(options.Name),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2 * time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			slog.Warn("NATS disconnected", "error", err)
		}),
		nats.ReconnectHandler(func(connection *nats.Conn) {
			slog.Info("NATS reconnected", "url", connection.ConnectedUrl())
		}),
	}
	if options.CredentialsFile != "" {
		dialOptions = append(dialOptions, nats.UserCredentials(options.CredentialsFile))
	}
	if options.CAFile != "" {
		dialOptions = append(dialOptions, nats.RootCAs(options.CAFile))
	}

	for attempt := 1; ; attempt++ {
		connection, err := nats.Connect(options.URL, dialOptions...)
		if err == nil {
			slog.Info("connected to NATS",
				"url", connection.ConnectedUrl(),
				"authenticated", options.CredentialsFile != "",
				"tls", connection.TLSRequired(),
			)
			return connection, nil
		}
		slog.Warn("NATS unavailable; retrying", "error", err, "url", options.URL, "attempt", attempt)
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("connect to NATS at %s: %w", options.URL, ctx.Err())
		case <-time.After(2 * time.Second):
		}
	}
}
