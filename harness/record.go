package harness

import (
	"context"
	"time"
	"uuid"
)

// Record is what a session keeps of one ended exchange: its IDs, its request and result, and
// the harness's own entries the exchange appended. The entries bind the driver's exchange ID
// to the harness's durable record of the session, which survives the harness process.
type Record struct {
	SessionID  string    `json:"sessionId"`
	ExchangeID uuid.UUID `json:"exchangeId"`
	Request    Request   `json:"request"`
	Result     Result    `json:"result"`
	// Err is the error the exchange ended in, if any.
	Err string `json:"err,omitempty"`
	// Entries are the IDs of the harness entries the exchange appended, in append order. They
	// are empty when the harness keeps no Journal, or exited before the exchange ended.
	Entries []string  `json:"entries,omitempty"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended"`
}

// Store keeps exchange records, so a session's exchange IDs outlive the process that
// assigned them. An implementation may keep them in files, a database, or anywhere else.
type Store interface {
	// Put adds rec to its session's records.
	Put(ctx context.Context, rec Record) error
	// Records returns a session's records in the order they were put, and none for a session
	// it has no records of.
	Records(ctx context.Context, sessionID string) ([]Record, error)
}
