// SPDX-License-Identifier: AGPL-3.0-or-later

package api

import (
	"context"
	"log/slog"
	"net/http"
	"sync"
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

// statusLabel is the status a request is counted under. A quota refusal is counted under its own label, not as a 5xx
// server failure: it answers 507 to the client, but it is the storage limit doing its job (see finish).
func (m *metrics) observe(method, route, statusLabel string, d time.Duration, bytes int64) {
	if !knownMethods[method] {
		method = "other"
	}
	m.requests.WithLabelValues(method, route, statusLabel).Inc()
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

// webhookCollector reports the webhook worker's queue at each scrape (design section 10). Its labels are the delivery
// states and the disable reasons, never a URL or an endpoint ID.
type webhookCollector struct {
	st       *store.Store
	log      *slog.Logger
	depth    *prometheus.Desc
	states   *prometheus.Desc
	disabled *prometheus.Desc
	now      func() time.Time
	// mu guards the cached queue stats: a scrape reads the queue at most once per webhookStatsTTL (review finding 8).
	mu        sync.Mutex
	cached    store.WebhookStats
	cachedAt  time.Time
	haveCache bool
}

// webhookStatsTTL is how long a read of the webhook queue stands in for the next scrape. The read costs about 110 ms
// at a million deliveries, measured in internal/store (TestWebhookScaleMeasure), so a scrape per 5 s would hold a
// reader for a fifth of the time.
const webhookStatsTTL = 30 * time.Second

// queueStats returns the webhook queue stats, read from the store unless the cached read is less than webhookStatsTTL
// old.
func (c *webhookCollector) queueStats(ctx context.Context) (store.WebhookStats, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	if c.haveCache && now.Sub(c.cachedAt) < webhookStatsTTL {
		return c.cached, nil
	}
	st, err := c.st.WebhookQueueStats(ctx)
	if err != nil {
		return st, err
	}
	c.cached, c.cachedAt, c.haveCache = st, now, true
	return st, nil
}

func newWebhookCollector(st *store.Store, log *slog.Logger) *webhookCollector {
	return &webhookCollector{
		st:  st,
		log: log,
		now: time.Now,
		depth: prometheus.NewDesc("selfcloud_webhook_queue_depth",
			"Pending webhook deliveries, over all endpoints.", nil, nil),
		states: prometheus.NewDesc("selfcloud_webhook_deliveries",
			"Webhook deliveries held by the outbox, by state.", []string{"state"}, nil),
		disabled: prometheus.NewDesc("selfcloud_webhook_endpoints_disabled",
			"Disabled webhook endpoints, by reason.", []string{"reason"}, nil),
	}
}

// Describe implements prometheus.Collector.
func (c *webhookCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.depth
	ch <- c.states
	ch <- c.disabled
}

// Collect implements prometheus.Collector. A failed read reports nothing rather than a false zero.
func (c *webhookCollector) Collect(ch chan<- prometheus.Metric) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	st, err := c.queueStats(ctx)
	if err != nil {
		c.log.Warn("metrics: cannot read the webhook queue", "error", err)
		return
	}
	ch <- prometheus.MustNewConstMetric(c.depth, prometheus.GaugeValue, float64(st.QueueDepth))
	for _, state := range []string{"pending", "delivered", "failed"} {
		ch <- prometheus.MustNewConstMetric(c.states, prometheus.GaugeValue, float64(st.Deliveries[state]), state)
	}
	for _, reason := range []string{store.WebhookDisabledFailing, store.WebhookDisabledOverflow, store.WebhookDisabledSecret} {
		ch <- prometheus.MustNewConstMetric(c.disabled, prometheus.GaugeValue, float64(st.DisabledEndpoints[reason]), reason)
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
