package ingest

import (
	"strings"

	"github.com/orvolt/orvolt/cloud/control-plane/internal/contract"
	"github.com/orvolt/orvolt/cloud/control-plane/internal/domain"
)

// Devices publish to their own subject below the base one:
//
//	orvolt.telemetry.evse.v1.edge-0001
//
// NATS enforces which subjects a credential is allowed to publish to, so the
// trailing token is an authenticated statement of who sent the message. The
// edge_id *inside* the payload is not: any device that can reach the bus could
// claim to be any other. Comparing the two is what turns "the payload says it
// is edge-0001" into "the bus confirmed it is edge-0001".
//
// A subject with no device token means the publisher was not identified. That
// is acceptable on an isolated development network and never acceptable for a
// deployment reachable from an untrusted one, which is why the strictness is a
// deployment decision rather than a code-level assumption.

// Verifier checks a decoded value against the subject it arrived on.
type Verifier[T any] func(subject string, value T) error

// VerifyTelemetryOrigin rejects telemetry whose claimed edge identity does not
// match the identity the bus authenticated.
func VerifyTelemetryOrigin(baseSubject string, required bool) Verifier[domain.Telemetry] {
	prefix := baseSubject + "."
	return func(subject string, telemetry domain.Telemetry) error {
		if subject == baseSubject || !strings.HasPrefix(subject, prefix) {
			if required {
				return &contract.PermanentError{
					Reason: "telemetry arrived on an unidentified subject; device identity is required",
				}
			}
			return nil
		}
		claimed := strings.TrimPrefix(subject, prefix)
		if claimed != telemetry.EdgeID {
			return &contract.PermanentError{
				Reason: "telemetry edge identity does not match the authenticated publisher",
			}
		}
		return nil
	}
}
