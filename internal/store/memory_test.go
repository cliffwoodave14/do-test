package store

import (
	"context"
	"testing"
	"time"
)

func mkEvent(id string, st Status, typ string, recv time.Time) Event {
	return Event{ID: id, Type: typ, Source: "test", Status: st, ReceivedAt: recv}
}

func TestMemory_SaveGet(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()

	if _, err := m.Get(ctx, "missing"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound, got %v", err)
	}

	e := mkEvent("a", StatusPending, "click", time.Now())
	if err := m.Save(ctx, e); err != nil {
		t.Fatalf("save: %v", err)
	}
	got, err := m.Get(ctx, "a")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.ID != "a" || got.Status != StatusPending {
		t.Fatalf("unexpected event: %+v", got)
	}
}

func TestMemory_Delete(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	_ = m.Save(ctx, mkEvent("a", StatusPending, "click", time.Now()))

	if err := m.Delete(ctx, "a"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := m.Get(ctx, "a"); err != ErrNotFound {
		t.Fatalf("want ErrNotFound after delete, got %v", err)
	}
	// Deleting a missing id is idempotent.
	if err := m.Delete(ctx, "missing"); err != nil {
		t.Fatalf("delete missing should be no-op, got %v", err)
	}
}

func TestMemory_ListFilterAndPaginate(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	base := time.Now()
	// Insert out of order to verify newest-first sorting.
	_ = m.Save(ctx, mkEvent("old", StatusProcessed, "click", base))
	_ = m.Save(ctx, mkEvent("new", StatusProcessed, "view", base.Add(time.Minute)))
	_ = m.Save(ctx, mkEvent("pending", StatusPending, "click", base.Add(2*time.Minute)))

	tests := []struct {
		name   string
		params ListParams
		wantID []string
	}{
		{"all newest first", ListParams{}, []string{"pending", "new", "old"}},
		{"filter processed", ListParams{Status: StatusProcessed}, []string{"new", "old"}},
		{"limit 1", ListParams{Limit: 1}, []string{"pending"}},
		{"offset past end", ListParams{Offset: 99}, []string{}},
		{"offset+limit", ListParams{Offset: 1, Limit: 1}, []string{"new"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := m.List(ctx, tt.params)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(got) != len(tt.wantID) {
				t.Fatalf("want %d events, got %d", len(tt.wantID), len(got))
			}
			for i, id := range tt.wantID {
				if got[i].ID != id {
					t.Errorf("pos %d: want %s, got %s", i, id, got[i].ID)
				}
			}
		})
	}
}

func TestMemory_Stats(t *testing.T) {
	ctx := context.Background()
	m := NewMemory()
	_ = m.Save(ctx, mkEvent("a", StatusProcessed, "click", time.Now()))
	_ = m.Save(ctx, mkEvent("b", StatusProcessed, "click", time.Now()))
	_ = m.Save(ctx, mkEvent("c", StatusFailed, "view", time.Now()))

	s, err := m.Stats(ctx)
	if err != nil {
		t.Fatalf("stats: %v", err)
	}
	if s.Total != 3 {
		t.Errorf("total: want 3, got %d", s.Total)
	}
	if s.ByStatus[StatusProcessed] != 2 || s.ByStatus[StatusFailed] != 1 {
		t.Errorf("by_status wrong: %+v", s.ByStatus)
	}
	if s.ByType["click"] != 2 || s.ByType["view"] != 1 {
		t.Errorf("by_type wrong: %+v", s.ByType)
	}
}
