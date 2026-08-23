// Package identity turns credentials presented at a charge point into values
// the platform can safely store.
//
// An OCPP idTag is usually an RFID card number. It identifies a person, and
// anyone holding it can start a session on that account. Billing and support
// need to recognise the same card again; neither needs to be able to reproduce
// it. A keyed hash gives exactly that: stable, comparable, and useless to an
// attacker who reads the database.
package identity

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

// MinimumKeyBytes rejects a key short enough to be brute-forced. Without a
// keyed construction, an unsalted hash of a short numeric card id is trivially
// reversible by enumeration.
const MinimumKeyBytes = 32

type Hasher struct {
	key []byte
}

// NewHasher fails closed: a gateway without a pepper would otherwise write
// reversible identifiers into permanent billing records.
func NewHasher(key string) (*Hasher, error) {
	if len(key) < MinimumKeyBytes {
		return nil, errors.New("token pepper must be at least 32 bytes; refusing to store weakly protected identifiers")
	}
	return &Hasher{key: []byte(key)}, nil
}

// Hash returns the lowercase hex HMAC-SHA256 of a presented token.
// An empty token hashes to an empty string, because "no credential was
// presented" is different from "this credential".
func (hasher *Hasher) Hash(token string) string {
	token = strings.TrimSpace(token)
	if token == "" {
		return ""
	}
	mac := hmac.New(sha256.New, hasher.key)
	mac.Write([]byte(strings.ToUpper(token)))
	return hex.EncodeToString(mac.Sum(nil))
}
