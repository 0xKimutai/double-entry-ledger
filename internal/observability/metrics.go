package observability

import (
	"database/sql"
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

type Metrics struct {
	registry      *prometheus.Registry
	requests      *prometheus.CounterVec
	requestsBytes *prometheus.CounterVec
	duration      *prometheus.HistogramVec
}

func NewMetrics(db *sql.DB) *Metrics {
	registry := prometheus.NewRegistry()
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "ledger",
		Subsystem: "http",
		Name:      "requests_total",
		Help:      "Total number of completed HTTP requests.",
	}, []string{"method", "route", "status"})
	requestsBytes := prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: "ledger",
		Subsystem: "http",
		Name:      "response_bytes_total",
		Help:      "Total number of response bytes written by the API.",
	}, []string{"method", "route"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: "ledger",
		Subsystem: "http",
		Name:      "request_duration_seconds",
		Help:      "HTTP request duration in seconds.",
		Buckets:   prometheus.DefBuckets,
	}, []string{"method", "route"})

	collectorsToRegister := []prometheus.Collector{
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
		requests,
		requestsBytes,
		duration,
	}
	if db != nil {
		collectorsToRegister = append(collectorsToRegister, &databaseStatsCollector{db: db})
	}
	registry.MustRegister(collectorsToRegister...)
	return &Metrics{
		registry:      registry,
		requests:      requests,
		requestsBytes: requestsBytes,
		duration:      duration,
	}
}

type databaseStatsCollector struct {
	db *sql.DB
}

func (*databaseStatsCollector) Describe(ch chan<- *prometheus.Desc) {
	for _, desc := range databaseStatsDescriptions {
		ch <- desc
	}
}

func (c *databaseStatsCollector) Collect(ch chan<- prometheus.Metric) {
	stats := c.db.Stats()
	values := []struct {
		desc  *prometheus.Desc
		value float64
	}{
		{databaseStatsDescriptions[0], float64(stats.OpenConnections)},
		{databaseStatsDescriptions[1], float64(stats.InUse)},
		{databaseStatsDescriptions[2], float64(stats.Idle)},
		{databaseStatsDescriptions[3], float64(stats.MaxOpenConnections)},
	}
	for _, item := range values {
		ch <- prometheus.MustNewConstMetric(item.desc, prometheus.GaugeValue, item.value)
	}
	ch <- prometheus.MustNewConstMetric(databaseStatsDescriptions[4], prometheus.CounterValue, float64(stats.WaitCount))
	ch <- prometheus.MustNewConstMetric(databaseStatsDescriptions[5], prometheus.CounterValue, stats.WaitDuration.Seconds())
	ch <- prometheus.MustNewConstMetric(databaseStatsDescriptions[6], prometheus.CounterValue, float64(stats.MaxIdleClosed))
	ch <- prometheus.MustNewConstMetric(databaseStatsDescriptions[7], prometheus.CounterValue, float64(stats.MaxLifetimeClosed))
}

var databaseStatsDescriptions = []*prometheus.Desc{
	prometheus.NewDesc("ledger_postgres_pool_open_connections", "Current open PostgreSQL connections.", nil, nil),
	prometheus.NewDesc("ledger_postgres_pool_in_use_connections", "PostgreSQL connections currently in use.", nil, nil),
	prometheus.NewDesc("ledger_postgres_pool_idle_connections", "Idle PostgreSQL connections.", nil, nil),
	prometheus.NewDesc("ledger_postgres_pool_max_open_connections", "Configured maximum open PostgreSQL connections.", nil, nil),
	prometheus.NewDesc("ledger_postgres_pool_wait_count_total", "Total waits for a PostgreSQL connection.", nil, nil),
	prometheus.NewDesc("ledger_postgres_pool_wait_duration_seconds_total", "Total time spent waiting for PostgreSQL connections.", nil, nil),
	prometheus.NewDesc("ledger_postgres_pool_max_idle_closed_total", "Connections closed because the maximum idle connection count was reached.", nil, nil),
	prometheus.NewDesc("ledger_postgres_pool_max_lifetime_closed_total", "Connections closed because their maximum lifetime was reached.", nil, nil),
}

func (m *Metrics) Observe(method, route string, status, responseBytes int, seconds float64) {
	switch method {
	case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodHead, http.MethodOptions:
	default:
		method = "OTHER"
	}
	statusCode := http.StatusText(status)
	if statusCode == "" {
		statusCode = "unknown"
	}
	m.requests.WithLabelValues(method, route, statusCode).Inc()
	m.requestsBytes.WithLabelValues(method, route).Add(float64(responseBytes))
	m.duration.WithLabelValues(method, route).Observe(seconds)
}

func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

var _ prometheus.Collector = (*databaseStatsCollector)(nil)
