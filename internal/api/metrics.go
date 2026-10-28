package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// metrics holds the server's Prometheus collectors. Labels come from the route table, never
// from raw paths, so their number stays bounded. Per-account figures belong to the usage API.
type metrics struct {
	reg      *prometheus.Registry
	requests *prometheus.CounterVec
	duration *prometheus.HistogramVec
	sent     *prometheus.CounterVec
	served   http.Handler
}

func newMetrics() *metrics {
	m := &metrics{
		reg: prometheus.NewRegistry(),
		requests: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "selfcloud",
			Name:      "http_requests_total",
			Help:      "HTTP requests by method, route and status.",
		}, []string{"method", "route", "status"}),
		duration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Namespace: "selfcloud",
			Name:      "http_request_duration_seconds",
			Help:      "HTTP request duration by method and route.",
			// Uploads and downloads of large files take minutes; the default buckets stop at 10s.
			Buckets: []float64{.005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5, 10, 30, 60, 300, 900},
		}, []string{"method", "route"}),
		sent: prometheus.NewCounterVec(prometheus.CounterOpts{
			Namespace: "selfcloud",
			Name:      "http_response_bytes_total",
			Help:      "Response body bytes sent, by route.",
		}, []string{"route"}),
	}
	m.reg.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		m.requests, m.duration, m.sent,
	)
	m.served = promhttp.HandlerFor(m.reg, promhttp.HandlerOpts{})
	return m
}

func (m *metrics) handler() http.Handler { return m.served }

var knownMethods = map[string]bool{
	http.MethodGet: true, http.MethodHead: true, http.MethodPost: true, http.MethodPut: true,
	http.MethodPatch: true, http.MethodDelete: true, http.MethodOptions: true,
}

func (m *metrics) observe(method, route string, status int, d time.Duration, bytes int64) {
	if !knownMethods[method] {
		method = "other"
	}
	m.requests.WithLabelValues(method, route, strconv.Itoa(status)).Inc()
	m.duration.WithLabelValues(method, route).Observe(d.Seconds())
	m.sent.WithLabelValues(route).Add(float64(bytes))
}
