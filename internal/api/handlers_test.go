package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/example/ingest-service/internal/ingest"
	"github.com/example/ingest-service/internal/store"
	"github.com/prometheus/client_golang/prometheus"
)

// newTestServer wires a real router over an in-memory store and live workers,
// so these are end-to-end handler tests, not mocks.
func newTestServer(t *testing.T) (http.Handler, store.Store, func()) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	st := store.NewMemory()
	svc := ingest.New(st, log, 2, 16)
	svc.Start()
	h := NewHandler(svc, st, log, func() bool { return true })
	router := NewRouter(h, log, prometheus.NewRegistry())
	cleanup := func() { _ = svc.Stop(context.Background()) }
	return router, st, cleanup
}

func TestCreateEvent_Accepted(t *testing.T) {
	router, _, cleanup := newTestServer(t)
	defer cleanup()

	body := `{"type":"click","source":"web","payload":{"value":10}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusAccepted {
		t.Fatalf("want 202, got %d (%s)", rec.Code, rec.Body.String())
	}
	var got store.Event
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.ID == "" || got.Status != store.StatusPending {
		t.Fatalf("unexpected response: %+v", got)
	}
	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("expected X-Request-ID header")
	}
}

func TestCreateEvent_ValidationError(t *testing.T) {
	router, _, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewBufferString(`{"type":""}`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("want 422, got %d", rec.Code)
	}
	var eb errorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &eb); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(eb.Details) == 0 {
		t.Error("expected validation details")
	}
}

func TestCreateEvent_BadJSON(t *testing.T) {
	router, _, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewBufferString(`{not json`))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400, got %d", rec.Code)
	}
}

func TestCreateEvent_TrailingJSON(t *testing.T) {
	router, _, cleanup := newTestServer(t)
	defer cleanup()

	// Two concatenated objects must be rejected, not silently accepted.
	body := `{"type":"a","source":"web","payload":{"k":1}}{"type":"b"}`
	req := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("want 400 for trailing JSON, got %d", rec.Code)
	}
}

func TestListEvents_BadParams(t *testing.T) {
	router, _, cleanup := newTestServer(t)
	defer cleanup()

	cases := []string{
		"/v1/events?limit=abc",
		"/v1/events?offset=xyz",
		"/v1/events?status=bogus",
	}
	for _, path := range cases {
		t.Run(path, func(t *testing.T) {
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
			if rec.Code != http.StatusBadRequest {
				t.Errorf("%s: want 400, got %d", path, rec.Code)
			}
		})
	}
}

func TestGetEvent_NotFound(t *testing.T) {
	router, _, cleanup := newTestServer(t)
	defer cleanup()

	req := httptest.NewRequest(http.MethodGet, "/v1/events/does-not-exist", nil)
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("want 404, got %d", rec.Code)
	}
}

func TestEventLifecycle(t *testing.T) {
	router, _, cleanup := newTestServer(t)
	defer cleanup()

	// Create.
	body := `{"type":"Click","source":"web","payload":{"value":21}}`
	req := httptest.NewRequest(http.MethodPost, "/v1/events", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var created store.Event
	_ = json.Unmarshal(rec.Body.Bytes(), &created)

	// Poll until processed via the public API.
	deadline := time.Now().Add(2 * time.Second)
	var final store.Event
	for time.Now().Before(deadline) {
		r := httptest.NewRecorder()
		router.ServeHTTP(r, httptest.NewRequest(http.MethodGet, "/v1/events/"+created.ID, nil))
		_ = json.Unmarshal(r.Body.Bytes(), &final)
		if final.Status == store.StatusProcessed {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if final.Status != store.StatusProcessed {
		t.Fatalf("event never processed: %+v", final)
	}
	if final.Result["doubled_value"] != float64(42) {
		t.Errorf("doubled_value: want 42, got %v", final.Result["doubled_value"])
	}
}

func TestHealthAndReady(t *testing.T) {
	router, _, cleanup := newTestServer(t)
	defer cleanup()

	for _, path := range []string{"/healthz", "/readyz"} {
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusOK {
			t.Errorf("%s: want 200, got %d", path, rec.Code)
		}
	}
}

func TestMetricsExposed(t *testing.T) {
	router, _, cleanup := newTestServer(t)
	defer cleanup()

	// Generate one request so a counter exists.
	router.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/v1/stats", nil))

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/metrics", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if !bytes.Contains(rec.Body.Bytes(), []byte("http_requests_total")) {
		t.Error("expected http_requests_total in /metrics output")
	}
}
