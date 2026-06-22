package store

import (
	"context"
	"sort"
	"sync"
)

// Memory is an in-memory Store implementation backed by a map and an
// RWMutex. It is safe for concurrent use.
type Memory struct {
	mu     sync.RWMutex
	events map[string]Event
}

// NewMemory returns an empty in-memory store.
func NewMemory() *Memory {
	return &Memory{events: make(map[string]Event)}
}

// Save inserts or replaces an event by id.
func (m *Memory) Save(_ context.Context, e Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.events[e.ID] = e
	return nil
}

// Get returns the event for id, or ErrNotFound.
func (m *Memory) Get(_ context.Context, id string) (Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	e, ok := m.events[id]
	if !ok {
		return Event{}, ErrNotFound
	}
	return e, nil
}

// Delete removes an event by id. Deleting a missing id is a no-op (idempotent).
func (m *Memory) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.events, id)
	return nil
}

// List returns events filtered by status and paginated, newest first.
func (m *Memory) List(_ context.Context, p ListParams) ([]Event, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Event, 0, len(m.events))
	for _, e := range m.events {
		if p.Status != "" && e.Status != p.Status {
			continue
		}
		out = append(out, e)
	}

	// Deterministic order: newest received first.
	sort.Slice(out, func(i, j int) bool {
		return out[i].ReceivedAt.After(out[j].ReceivedAt)
	})

	// Pagination with bounds checks.
	if p.Offset >= len(out) {
		return []Event{}, nil
	}
	end := len(out)
	if p.Limit > 0 && p.Offset+p.Limit < end {
		end = p.Offset + p.Limit
	}
	return out[p.Offset:end], nil
}

// Stats returns aggregate counts across all events.
func (m *Memory) Stats(_ context.Context) (Stats, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	s := Stats{
		Total:    len(m.events),
		ByStatus: make(map[Status]int),
		ByType:   make(map[string]int),
	}
	for _, e := range m.events {
		s.ByStatus[e.Status]++
		s.ByType[e.Type]++
	}
	return s, nil
}
