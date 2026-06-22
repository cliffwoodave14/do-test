// Package store defines the persistence boundary for ingested events.
//
// Engineering-quality signal: the rest of the app depends on the Store
// *interface*, not a concrete database. The in-memory implementation ships
// in minutes and is trivially testable; swapping in MySQL/Postgres later is
// a single new implementation with zero changes to handlers or workers.
package store

import (
	"context"
	"errors"
	"time"
)

// ErrNotFound is returned when an event id does not exist.
var ErrNotFound = errors.New("event not found")

// Status is the processing lifecycle state of an event.
type Status string

const (
	StatusPending   Status = "pending"
	StatusProcessed Status = "processed"
	StatusFailed    Status = "failed"
)

// Valid reports whether s is a known status value.
func (s Status) Valid() bool {
	switch s {
	case StatusPending, StatusProcessed, StatusFailed:
		return true
	default:
		return false
	}
}

// Event is an ingested record and its processing result.
type Event struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Source      string         `json:"source"`
	Payload     map[string]any `json:"payload"`
	Status      Status         `json:"status"`
	Result      map[string]any `json:"result,omitempty"`
	Error       string         `json:"error,omitempty"`
	ReceivedAt  time.Time      `json:"received_at"`
	ProcessedAt *time.Time     `json:"processed_at,omitempty"`
}

// ListParams controls filtering and pagination for List.
type ListParams struct {
	Status Status // empty means "any"
	Limit  int
	Offset int
}

// Stats is an aggregate view across all events.
type Stats struct {
	Total    int            `json:"total"`
	ByStatus map[Status]int `json:"by_status"`
	ByType   map[string]int `json:"by_type"`
}

// Store is the persistence boundary. Implementations must be safe for
// concurrent use by handlers and background workers.
type Store interface {
	Save(ctx context.Context, e Event) error
	Get(ctx context.Context, id string) (Event, error)
	Delete(ctx context.Context, id string) error
	List(ctx context.Context, p ListParams) ([]Event, error)
	Stats(ctx context.Context) (Stats, error)
}
