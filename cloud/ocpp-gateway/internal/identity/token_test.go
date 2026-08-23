package identity_test

import (
	"strings"
	"testing"

	"github.com/orvolt/orvolt/cloud/ocpp-gateway/internal/identity"
)

const pepper = "an-example-pepper-of-at-least-32-bytes"

// A gateway without a pepper would write reversible card identifiers into
// permanent billing records, so it must refuse to start rather than degrade.
func TestWeakPepperIsRefused(t *testing.T) {
	for _, key := range []string{"", "short", strings.Repeat("x", identity.MinimumKeyBytes-1)} {
		if _, err := identity.NewHasher(key); err == nil {
			t.Errorf("expected a pepper of %d bytes to be refused: %q", len(key), key)
		}
	}
	if _, err := identity.NewHasher(pepper); err != nil {
		t.Fatalf("a sufficient pepper must be accepted: %v", err)
	}
}

func TestHashIsStableAndDoesNotRevealTheToken(t *testing.T) {
	hasher, err := identity.NewHasher(pepper)
	if err != nil {
		t.Fatal(err)
	}

	first := hasher.Hash("04A1B2C3")
	second := hasher.Hash("04A1B2C3")
	if first != second {
		t.Fatal("the same card must hash to the same value, or nothing can recognise it again")
	}
	if strings.Contains(first, "04A1B2C3") {
		t.Fatal("the raw token leaked into the hash")
	}
	if len(first) != 64 {
		t.Fatalf("expected a hex-encoded SHA-256, got %d characters", len(first))
	}
	if hasher.Hash("04A1B2C4") == first {
		t.Fatal("different cards must not collide")
	}
}

// Charge points differ on the case they present a card in; the same physical
// card must not produce two customers.
func TestHashIsCaseInsensitive(t *testing.T) {
	hasher, err := identity.NewHasher(pepper)
	if err != nil {
		t.Fatal(err)
	}
	if hasher.Hash("04a1b2c3") != hasher.Hash("04A1B2C3") {
		t.Fatal("the same card presented in a different case must hash identically")
	}
}

// "No credential was presented" is different from "this credential".
func TestEmptyTokenStaysEmpty(t *testing.T) {
	hasher, err := identity.NewHasher(pepper)
	if err != nil {
		t.Fatal(err)
	}
	if hasher.Hash("") != "" || hasher.Hash("   ") != "" {
		t.Fatal("an absent token must not be given an identity")
	}
}

// A different deployment must not be able to correlate its users with another's.
func TestDifferentPeppersProduceDifferentIdentities(t *testing.T) {
	first, err := identity.NewHasher(pepper)
	if err != nil {
		t.Fatal(err)
	}
	second, err := identity.NewHasher(strings.Repeat("y", 40))
	if err != nil {
		t.Fatal(err)
	}
	if first.Hash("04A1B2C3") == second.Hash("04A1B2C3") {
		t.Fatal("two deployments must not derive the same identifier for one card")
	}
}
