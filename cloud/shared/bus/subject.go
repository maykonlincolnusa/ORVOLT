package bus

import "fmt"

// MaxIdentityBytes is the longest identity that still fits comfortably in a
// subject token.
const MaxIdentityBytes = 128

// DeviceSubject addresses a publisher's own subject below a base one:
//
//	orvolt.telemetry.evse.v1.ocpp-gateway-001
//
// NATS enforces which subjects a credential may publish to, so this turns the
// broker's permission model into an authenticated statement of who sent each
// message. The control plane compares it against the identity inside the
// payload, which the publisher could otherwise set to anything.
//
// The identity is validated rather than escaped: a '.' would silently create an
// extra subject token, and a '*' or '>' would be a wildcard, either of which
// would let a careless identity read as a different publisher — or as all of
// them.
func DeviceSubject(base, identity string) (string, error) {
	if err := ValidateIdentity(identity); err != nil {
		return "", err
	}
	return base + "." + identity, nil
}

func ValidateIdentity(identity string) error {
	if identity == "" {
		return fmt.Errorf("identity must not be empty")
	}
	if len(identity) > MaxIdentityBytes {
		return fmt.Errorf("identity must be at most %d bytes", MaxIdentityBytes)
	}
	for _, character := range identity {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z',
			character >= '0' && character <= '9',
			character == '-', character == '_':
		default:
			return fmt.Errorf("identity may only contain letters, digits, '-' and '_': found %q", character)
		}
	}
	return nil
}
