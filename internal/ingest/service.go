// Package ingest contains the domain logic: validating incoming records and
// processing them asynchronously through a worker pool.
//
// This is the testable core of the service. It deliberately knows nothing
// about HTTP — handlers call Submit, workers drain the queue and persist
// results. The async pipeline maps directly to the JD's "asynchronous or
// event-driven processing" requirement.
package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/example/ingest-service/internal/store"
)

// ErrQueueFull is returned when the processing queue cannot accept more work,
// allowing the handler to apply backpressure (HTTP 503) instead of blocking.
var ErrQueueFull = errors.New("processing queue is full")

// NewEvent is the validated input for a single ingestion request.
type NewEvent struct {
	Type    string         `json:"type"`
	Source  string         `json:"source"`
	Payload map[string]any `json:"payload"`
}

// Validate enforces the input contract. Returns a ValidationError listing
// every problem so the client can fix them in one round trip.
func (n NewEvent) Validate() error {
	var problems []string
	if strings.TrimSpace(n.Type) == "" {
		problems = append(problems, "type is required")
	}
	if strings.TrimSpace(n.Source) == "" {
		problems = append(problems, "source is required")
	}
	if len(n.Payload) == 0 {
		problems = append(problems, "payload must be a non-empty object")
	}
	if len(problems) > 0 {
		return &ValidationError{Problems: problems}
	}
	return nil
}

// ValidationError carries field-level validation problems.
type ValidationError struct {
	Problems []string
}

func (e *ValidationError) Error() string {
	return "validation failed: " + strings.Join(e.Problems, "; ")
}

// IDGenerator produces unique event ids. Injected so tests are deterministic.
type IDGenerator func() string

// Clock returns the current time. Injected so tests are deterministic.
type Clock func() time.Time

// Service validates, persists, and asynchronously processes events.
type Service struct {
	store   store.Store
	log     *slog.Logger
	queue   chan string
	newID   IDGenerator
	now     Clock
	workers int

	wg sync.WaitGroup
}

// Option configures a Service.
type Option func(*Service)

// WithIDGenerator overrides the default id generator (useful in tests).
func WithIDGenerator(g IDGenerator) Option { return func(s *Service) { s.newID = g } }

// WithClock overrides the default clock (useful in tests).
func WithClock(c Clock) Option { return func(s *Service) { s.now = c } }

// New constructs a Service. Call Start to launch the worker pool.
func New(st store.Store, log *slog.Logger, workers, queueSize int, opts ...Option) *Service {
	s := &Service{
		store:   st,
		log:     log,
		queue:   make(chan string, queueSize),
		newID:   defaultID,
		now:     time.Now,
		workers: workers,
	}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Submit validates input, persists the event as pending, and enqueues it for
// processing. It returns the stored event immediately (HTTP 202 semantics).
func (s *Service) Submit(ctx context.Context, in NewEvent) (store.Event, error) {
	if err := in.Validate(); err != nil {
		return store.Event{}, err
	}

	e := store.Event{
		ID:         s.newID(),
		Type:       in.Type,
		Source:     in.Source,
		Payload:    in.Payload,
		Status:     store.StatusPending,
		ReceivedAt: s.now().UTC(),
	}
	if err := s.store.Save(ctx, e); err != nil {
		return store.Event{}, fmt.Errorf("persist pending event: %w", err)
	}

	select {
	case s.queue <- e.ID:
		return e, nil
	default:
		// Backpressure: do not block the request goroutine. Roll back the
		// pending record so a rejected submit leaves no orphaned event that
		// can never transition out of "pending".
		if derr := s.store.Delete(ctx, e.ID); derr != nil {
			s.log.Error("could not roll back orphaned pending event", "id", e.ID, "err", derr)
		}
		return store.Event{}, ErrQueueFull
	}
}

// Start launches the worker pool. Workers run until Stop is called.
func (s *Service) Start() {
	for i := 0; i < s.workers; i++ {
		s.wg.Add(1)
		go s.worker()
	}
	s.log.Info("ingest workers started", "count", s.workers)
}

// Stop closes the queue and waits for in-flight work to drain, bounded by the
// supplied context. Returns ctx.Err() if the drain deadline is exceeded.
func (s *Service) Stop(ctx context.Context) error {
	close(s.queue)
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		s.log.Info("ingest workers drained")
		return nil
	case <-ctx.Done():
		s.log.Warn("ingest drain timed out", "err", ctx.Err())
		return ctx.Err()
	}
}

func (s *Service) worker() {
	defer s.wg.Done()
	for eventID := range s.queue {
		s.processOne(context.Background(), eventID)
	}
}

// processOne loads, transforms, and persists the result for a single event.
func (s *Service) processOne(ctx context.Context, id string) {
	e, err := s.store.Get(ctx, id)
	if err != nil {
		s.log.Error("worker could not load event", "id", id, "err", err)
		return
	}

	result, perr := transform(e)
	now := s.now().UTC()
	e.ProcessedAt = &now
	if perr != nil {
		e.Status = store.StatusFailed
		e.Error = perr.Error()
	} else {
		e.Status = store.StatusProcessed
		e.Result = result
	}

	if err := s.store.Save(ctx, e); err != nil {
		s.log.Error("worker could not persist result", "id", id, "err", err)
		return
	}
	s.log.Debug("event processed", "id", id, "status", e.Status)
}

// transform is the placeholder business logic: enrich the payload with a
// derived score and a normalized type. Replace with the real processing
// the exercise asks for.
func transform(e store.Event) (map[string]any, error) {
	result := map[string]any{
		"normalized_type": strings.ToLower(strings.TrimSpace(e.Type)),
		"field_count":     len(e.Payload),
	}
	if v, ok := e.Payload["value"]; ok {
		f, ok := toFloat(v)
		if !ok {
			return nil, fmt.Errorf("payload.value must be numeric, got %T", v)
		}
		result["doubled_value"] = f * 2
	}
	return result, nil
}

func toFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}
