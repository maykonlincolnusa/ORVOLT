package domain

import (
	"context"
	"time"
)

// Session event types.
const (
	SessionStarted = "STARTED"
	SessionUpdated = "UPDATED"
	SessionStopped = "STOPPED"
)

// SessionEvent is one reported transition of a charging transaction.
//
// Events can arrive out of order or more than once: a charge point that loses
// its uplink mid-session replays what it buffered, and OCPP itself allows a
// StopTransaction to reach the server before the StartTransaction it closes.
// The projection is therefore built to tolerate both.
type SessionEvent struct {
	SessionID            string
	StationID            string
	ConnectorID          string
	SiteID               string
	Type                 string
	OccurredAt           time.Time
	IngestedAt           time.Time
	TokenType            string
	TokenHash            string
	AuthorizationRef     string
	EnergyRegisterWh     *int64
	StopReason           string
	SourceProtocol       string
	TransactionReference string
}

// Session is the projection used for billing, reporting and support.
type Session struct {
	SessionID            string     `json:"session_id"`
	StationID            string     `json:"station_id"`
	ConnectorID          string     `json:"connector_id"`
	SiteID               string     `json:"site_id"`
	SourceProtocol       string     `json:"source_protocol"`
	TransactionReference string     `json:"transaction_reference,omitempty"`
	TokenType            string     `json:"token_type"`
	TokenHash            string     `json:"token_hash,omitempty"`
	AuthorizationRef     string     `json:"authorization_reference,omitempty"`
	StartedAt            *time.Time `json:"started_at,omitempty"`
	StoppedAt            *time.Time `json:"stopped_at,omitempty"`
	MeterStartWh         *int64     `json:"meter_start_wh,omitempty"`
	MeterStopWh          *int64     `json:"meter_stop_wh,omitempty"`
	// EnergyDeliveredWh is derived from the register readings. It is nil while
	// the start reading is unknown, which is honest: reporting zero would be
	// indistinguishable from a session that genuinely delivered nothing.
	EnergyDeliveredWh *int64    `json:"energy_delivered_wh,omitempty"`
	StopReason        string    `json:"stop_reason,omitempty"`
	Open              bool      `json:"open"`
	UpdatedAt         time.Time `json:"updated_at"`
}

// SessionQuery bounds a session listing.
type SessionQuery struct {
	StationID string
	OpenOnly  bool
	Page      Page
}

type SessionRepository interface {
	PersistSessionEventBatch(context.Context, []SessionEvent) error
	GetSession(context.Context, string) (Session, bool, error)
	ListSessions(context.Context, SessionQuery) ([]Session, error)
}
