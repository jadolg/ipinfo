package main

import (
	"log"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ipinfo_http_requests_total",
		Help: "Total HTTP requests by method, path, and status code.",
	}, []string{"method", "path", "status"})

	httpRequestDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "ipinfo_http_request_duration_seconds",
		Help:    "HTTP request latency by method and path.",
		Buckets: prometheus.DefBuckets,
	}, []string{"method", "path"})

	ipVersionHits = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ipinfo_ip_version_hits_total",
		Help: "Total lookups by IP version (4 or 6).",
	}, []string{"version"})

	errorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "ipinfo_errors_total",
		Help: "Total errors by component and operation.",
	}, []string{"component", "operation"})
)

// recordError increments the errors counter for the given component and operation.
func recordError(component, operation string) {
	errorsTotal.WithLabelValues(component, operation).Inc()
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

// withMetrics wraps a handler and records request count and duration.
// pattern is the route label (e.g. "/json") used in metric labels.
func withMetrics(pattern string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		h(rec, r)
		httpRequestsTotal.WithLabelValues(r.Method, pattern, strconv.Itoa(rec.status)).Inc()
		httpRequestDuration.WithLabelValues(r.Method, pattern).Observe(time.Since(start).Seconds())
	}
}

func startMetricsServer(addr string) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	srv := &http.Server{
		Addr:        addr,
		Handler:     mux,
		ReadTimeout: 5 * time.Second,
	}
	go func() {
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server error: %v", err)
		}
	}()
	log.Printf("metrics listening on %s/metrics", addr)
}
