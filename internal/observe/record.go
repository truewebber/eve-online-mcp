package observe

import (
	"strconv"
	"time"
)

func (r *Registry) Request(method string, status int, path string, d time.Duration) {
	if r == nil {
		return
	}
	labels := []string{method, strconv.Itoa(status), path}
	r.esiRequests.WithLabelValues(labels...).Inc()
	r.esiDuration.WithLabelValues(labels...).Observe(d.Seconds())
}

func (r *Registry) HTTP(method string, status int, path string, d time.Duration) {
	if r == nil {
		return
	}
	labels := []string{method, strconv.Itoa(status), path}
	r.httpRequests.WithLabelValues(labels...).Inc()
	r.httpDuration.WithLabelValues(labels...).Observe(d.Seconds())
}
