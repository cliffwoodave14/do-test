package api

import (
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/example/ingest-service/internal/ingest"
	"github.com/example/ingest-service/internal/store"
)

// Handler holds the dependencies the HTTP handlers need.
type Handler struct {
	svc   *ingest.Service
	store store.Store
	log   *slog.Logger
	ready func() bool
}

// NewHandler wires the API handlers. ready reports readiness for /readyz.
func NewHandler(svc *ingest.Service, st store.Store, log *slog.Logger, ready func() bool) *Handler {
	return &Handler{svc: svc, store: st, log: log, ready: ready}
}

const maxBodyBytes = 1 << 20 // 1 MiB cap to bound memory per request.

// createEvent handles POST /v1/events.
func (h *Handler) createEvent(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()

	var in ingest.NewEvent
	if err := dec.Decode(&in); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON body: "+err.Error())
		return
	}
	// Reject a body with trailing content after the first JSON object so two
	// concatenated payloads can't be silently accepted as one.
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		writeError(w, http.StatusBadRequest, "request body must contain a single JSON object")
		return
	}

	e, err := h.svc.Submit(r.Context(), in)
	switch {
	case err == nil:
		writeJSON(w, http.StatusAccepted, e)
	case isValidation(err):
		writeErrorDetails(w, http.StatusUnprocessableEntity, "validation failed", validationProblems(err))
	case errors.Is(err, ingest.ErrQueueFull):
		w.Header().Set("Retry-After", "1")
		writeError(w, http.StatusServiceUnavailable, "server busy, retry shortly")
	default:
		h.log.Error("submit failed", "err", err)
		writeError(w, http.StatusInternalServerError, "could not accept event")
	}
}

// getEvent handles GET /v1/events/{id}.
func (h *Handler) getEvent(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	e, err := h.store.Get(r.Context(), id)
	if errors.Is(err, store.ErrNotFound) {
		writeError(w, http.StatusNotFound, "event not found")
		return
	}
	if err != nil {
		h.log.Error("get failed", "err", err)
		writeError(w, http.StatusInternalServerError, "lookup failed")
		return
	}
	writeJSON(w, http.StatusOK, e)
}

// listEvents handles GET /v1/events?status=&limit=&offset=.
func (h *Handler) listEvents(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	limit, err := atoiParam(q.Get("limit"), 50)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid limit: must be an integer")
		return
	}
	offset, err := atoiParam(q.Get("offset"), 0)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid offset: must be an integer")
		return
	}
	status := store.Status(q.Get("status"))
	if status != "" && !status.Valid() {
		writeError(w, http.StatusBadRequest, "invalid status: must be one of pending, processed, failed")
		return
	}

	params := store.ListParams{Status: status, Limit: limit, Offset: offset}
	if params.Limit < 0 || params.Offset < 0 {
		writeError(w, http.StatusBadRequest, "limit and offset must be non-negative")
		return
	}
	if params.Limit > 500 {
		params.Limit = 500 // cap page size
	}

	events, err := h.store.List(r.Context(), params)
	if err != nil {
		h.log.Error("list failed", "err", err)
		writeError(w, http.StatusInternalServerError, "list failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"events": events,
		"count":  len(events),
		"limit":  params.Limit,
		"offset": params.Offset,
	})
}

// stats handles GET /v1/stats.
func (h *Handler) stats(w http.ResponseWriter, r *http.Request) {
	s, err := h.store.Stats(r.Context())
	if err != nil {
		h.log.Error("stats failed", "err", err)
		writeError(w, http.StatusInternalServerError, "stats failed")
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// healthz is a liveness probe: the process is up and serving.
func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// readyz is a readiness probe: dependencies are ready to take traffic.
func (h *Handler) readyz(w http.ResponseWriter, _ *http.Request) {
	if h.ready != nil && !h.ready() {
		writeError(w, http.StatusServiceUnavailable, "not ready")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

// atoiParam parses an optional integer query param, returning def when empty
// and an error when present-but-malformed (so the caller can return 400).
func atoiParam(s string, def int) (int, error) {
	if s == "" {
		return def, nil
	}
	return strconv.Atoi(s)
}

func isValidation(err error) bool {
	var ve *ingest.ValidationError
	return errors.As(err, &ve)
}

func validationProblems(err error) []string {
	var ve *ingest.ValidationError
	if errors.As(err, &ve) {
		return ve.Problems
	}
	return nil
}
