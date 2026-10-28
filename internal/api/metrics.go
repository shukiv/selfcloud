// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/shukiv/selfcloud/internal/store"
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

// totalsCollector reports the installation's totals of used and reserved bytes, read from the store
// at each scrape. Per-account figures come from the usage API, not from metric labels.
type totalsCollector struct {
	st       *store.Store
	log      *slog.Logger
	used     *prometheus.Desc
	reserved *prometheus.Desc
}

func newTotalsCollector(st *store.Store, log *slog.Logger) *totalsCollector {
	return &totalsCollector{
		st:  st,
		log: log,
		used: prometheus.NewDesc("selfcloud_storage_used_bytes",
			"Bytes of files held by all accounts.", nil, nil),
		reserved: prometheus.NewDesc("selfcloud_storage_reserved_bytes",
			"Declared lengths of unfinished uploads, summed over all accounts.", nil, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *totalsCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.used
	ch <- c.reserved
}

// Collect implements prometheus.Collector. A failed read reports nothing rather than a false zero.
func (c *totalsCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	used, reserved, err := c.st.UsageTotals(ctx)
	if err != nil {
		c.log.Warn("metrics: cannot read the usage totals", "error", err)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.used, prometheus.GaugeValue, float64(used))
	ch <- prometheus.MustNewConstMetric(c.reserved, prometheus.GaugeValue, float64(reserved))
}
