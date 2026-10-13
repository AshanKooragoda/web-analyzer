// Package metrics defines the Prometheus metrics exposed by the application.
package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Metrics holds the application's collectors.
type Metrics struct {
	Requests         *prometheus.CounterVec
	RequestDuration  *prometheus.HistogramVec
	Analyses         *prometheus.CounterVec
	AnalysisDuration prometheus.Histogram
	LinksChecked     prometheus.Counter
	LinksBroken      prometheus.Counter
}

// New creates the collectors and registers them with reg.
func New(reg prometheus.Registerer) *Metrics {
	f := promauto.With(reg)
	return &Metrics{
		Requests: f.NewCounterVec(prometheus.CounterOpts{
			Name: "http_requests_total",
			Help: "HTTP requests handled, by route and status code.",
		}, []string{"method", "route", "code"}),
		RequestDuration: f.NewHistogramVec(prometheus.HistogramOpts{
			Name:    "http_request_duration_seconds",
			Help:    "HTTP request latency, by route.",
			Buckets: prometheus.DefBuckets,
		}, []string{"method", "route"}),
		Analyses: f.NewCounterVec(prometheus.CounterOpts{
			Name: "analyses_total",
			Help: "Page analyses, by outcome (success, invalid_url, fetch_error, error).",
		}, []string{"outcome"}),
		AnalysisDuration: f.NewHistogram(prometheus.HistogramOpts{
			Name:    "analysis_duration_seconds",
			Help:    "Time taken to analyze a page, including link checks.",
			Buckets: []float64{0.25, 0.5, 1, 2.5, 5, 10, 20, 30, 45, 60},
		}),
		LinksChecked: f.NewCounter(prometheus.CounterOpts{
			Name: "links_checked_total",
			Help: "Links checked for accessibility.",
		}),
		LinksBroken: f.NewCounter(prometheus.CounterOpts{
			Name: "links_inaccessible_total",
			Help: "Links found to be inaccessible.",
		}),
	}
}
