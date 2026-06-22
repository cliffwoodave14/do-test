package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/example/ingest-service/internal/store"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestNewEvent_Validate(t *testing.T) {
	tests := []struct {
		name     string
		in       NewEvent
		wantErr  bool
		problems int
	}{
		{"valid", NewEvent{Type: "click", Source: "web", Payload: map[string]any{"k": 1}}, false, 0},
		{"missing all", NewEvent{}, true, 3},
		{"blank type", NewEvent{Type: "  ", Source: "web", Payload: map[string]any{"k": 1}}, true, 1},
		{"empty payload", NewEvent{Type: "click", Source: "web", Payload: map[string]any{}}, true, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.in.Validate()
			if tt.wantErr != (err != nil) {
				t.Fatalf("wantErr=%v, got %v", tt.wantErr, err)
			}
			if err != nil {
				ve, ok := err.(*ValidationError)
				if !ok {
					t.Fatalf("want *ValidationError, got %T", err)
				}
				if len(ve.Problems) != tt.problems {
					t.Errorf("want %d problems, got %d (%v)", tt.problems, len(ve.Problems), ve.Problems)
				}
			}
		})
	}
}

func TestService_SubmitAndProcess(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	fixed := time.Date(2026, 6, 20, 12, 0, 0, 0, time.UTC)
	svc := New(st, quietLogger(), 2, 16,
		WithIDGenerator(func() string { return "fixed-id" }),
		WithClock(func() time.Time { return fixed }),
	)
	svc.Start()
	defer svc.Stop(ctx) //nolint:errcheck

	e, err := svc.Submit(ctx, NewEvent{
		Type:    "Click",
		Source:  "web",
		Payload: map[string]any{"value": 21, "extra": "x"},
	})
	if err != nil {
		t.Fatalf("submit: %v", err)
	}
	if e.ID != "fixed-id" || e.Status != store.StatusPending {
		t.Fatalf("unexpected submitted event: %+v", e)
	}

	got := waitForStatus(t, st, "fixed-id", store.StatusProcessed)
	if got.Result["normalized_type"] != "click" {
		t.Errorf("normalized_type: want click, got %v", got.Result["normalized_type"])
	}
	if got.Result["doubled_value"] != float64(42) {
		t.Errorf("doubled_value: want 42, got %v", got.Result["doubled_value"])
	}
}

func TestService_ProcessFailsOnBadValue(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	svc := New(st, quietLogger(), 1, 4,
		WithIDGenerator(func() string { return "bad" }),
	)
	svc.Start()
	defer svc.Stop(ctx) //nolint:errcheck

	if _, err := svc.Submit(ctx, NewEvent{
		Type:    "click",
		Source:  "web",
		Payload: map[string]any{"value": "not-a-number"},
	}); err != nil {
		t.Fatalf("submit: %v", err)
	}

	got := waitForStatus(t, st, "bad", store.StatusFailed)
	if got.Error == "" {
		t.Errorf("expected an error message on failed event")
	}
}

func TestService_QueueFullRollsBack(t *testing.T) {
	ctx := context.Background()
	st := store.NewMemory()
	// Workers NOT started + queue size 1: the second submit cannot enqueue.
	ids := []string{"first", "second"}
	var i int
	svc := New(st, quietLogger(), 1, 1,
		WithIDGenerator(func() string { id := ids[i]; i++; return id }),
	)

	if _, err := svc.Submit(ctx, NewEvent{Type: "click", Source: "web", Payload: map[string]any{"k": 1}}); err != nil {
		t.Fatalf("first submit should succeed: %v", err)
	}
	_, err := svc.Submit(ctx, NewEvent{Type: "click", Source: "web", Payload: map[string]any{"k": 1}})
	if !errors.Is(err, ErrQueueFull) {
		t.Fatalf("want ErrQueueFull, got %v", err)
	}
	// The rejected event must not linger as a pending orphan.
	if _, gerr := st.Get(ctx, "second"); gerr != store.ErrNotFound {
		t.Fatalf("rejected event should be rolled back, got %v", gerr)
	}
}

func TestService_SubmitRejectsInvalid(t *testing.T) {
	svc := New(store.NewMemory(), quietLogger(), 1, 4)
	if _, err := svc.Submit(context.Background(), NewEvent{}); err == nil {
		t.Fatal("expected validation error, got nil")
	}
}

// waitForStatus polls the store until the event reaches want or the deadline
// passes. Async processing means we cannot assert synchronously.
func waitForStatus(t *testing.T, st store.Store, id string, want store.Status) store.Event {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		e, err := st.Get(context.Background(), id)
		if err == nil && e.Status == want {
			return e
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("event %s did not reach status %q in time", id, want)
	return store.Event{}
}
