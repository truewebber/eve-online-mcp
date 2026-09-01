package observe

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

const (
	ns          = "eve_mcp"
	labelMethod = "method"
	labelStatus = "status"
	labelPath   = "path"
)

// Registry is the process Prometheus registry. Constructed in main and
// injected; nothing reaches for a package-level default.
type Registry struct {
	reg *prometheus.Registry

	esiRequests  *prometheus.CounterVec
	esiDuration  *prometheus.HistogramVec
	httpRequests *prometheus.CounterVec
	httpDuration *prometheus.HistogramVec
}

func New() *Registry {
	r := &Registry{reg: prometheus.NewRegistry()}
	r.esiRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "esi_requests_total",
		Help: "Outbound ESI HTTP attempts this pod made. path is a template ({id}), never a raw id. Per pod.",
	}, []string{labelMethod, labelStatus, labelPath})
	r.esiDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: ns, Name: "esi_request_duration_seconds",
		Help: "Outbound ESI HTTP round-trip time this pod saw. path is a template. Per pod.",
	}, []string{labelMethod, labelStatus, labelPath})
	r.httpRequests = prometheus.NewCounterVec(prometheus.CounterOpts{
		Namespace: ns, Name: "http_requests_total",
		Help: "Public HTTP requests this pod served. path is the mux pattern, or other. Per pod.",
	}, []string{labelMethod, labelStatus, labelPath})
	r.httpDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Namespace: ns, Name: "http_request_duration_seconds",
		Help: "Public HTTP handler time this pod spent. path is the mux pattern, or other. Per pod.",
	}, []string{labelMethod, labelStatus, labelPath})
	r.reg.MustRegister(r.esiRequests, r.esiDuration, r.httpRequests, r.httpDuration)

	return r
}

func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.reg, promhttp.HandlerOpts{})
}
