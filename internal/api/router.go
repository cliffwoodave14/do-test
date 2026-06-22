package api

import (
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metrics holds the Prometheus collectors for the service.
type metrics struct {
	reqs    *prometheus.CounterVec
	latency *prometheus.HistogramVec
}

func newMetrics(reg prometheus.Registerer) *metrics {
	factory := promauto.With(reg)
	return &metrics{
		reqs: factory.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "Total HTTP requests by method, route, and status.",
		}, []string{"method", "route", "status"}),
		latency: factory.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency by route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
	}
}

// instrument records request count and latency for a named route. The route
// label is supplied explicitly (not from the URL path) to avoid unbounded
// cardinality from path parameters like event ids.
func (m *metrics) instrument(route string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h(rec, r)
		m.reqs.WithLabelValues(r.Method, route, strconv.Itoa(rec.status)).Inc()
		m.latency.WithLabelValues(r.Method, route).Observe(time.Since(start).Seconds())
	}
}

// NewRouter builds the fully wired HTTP handler: routes, metrics, and the
// middleware chain (request-id → logging → recovery).
func NewRouter(h *Handler, log *slog.Logger, reg *prometheus.Registry) http.Handler {
	m := newMetrics(reg)
	mux := http.NewServeMux()

	// API routes (Go 1.22+ method+pattern routing, no external router needed).
	mux.HandleFunc("POST /v1/events", m.instrument("create_event", h.createEvent))
	mux.HandleFunc("GET /v1/events", m.instrument("list_events", h.listEvents))
	mux.HandleFunc("GET /v1/events/{id}", m.instrument("get_event", h.getEvent))
	mux.HandleFunc("GET /v1/stats", m.instrument("stats", h.stats))

	// Operational endpoints (unmetered, no auth).
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /readyz", h.readyz)
	mux.Handle("GET /metrics", promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))

	return chain(mux, requestID, logging(log), recoverer(log))
}
