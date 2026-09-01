package httpsvc

import (
	"net/http"
	"strings"
	"time"
)

const otherRoute = "other"

type Observer interface {
	HTTP(method string, status int, path string, d time.Duration)
}

func observePublic(next http.Handler, obs Observer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		sw := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(sw, r)
		obs.HTTP(r.Method, sw.status, routeTemplate(r), time.Since(start))
	})
}

func routeTemplate(r *http.Request) string {
	p := r.Pattern
	if i := strings.IndexByte(p, ' '); i >= 0 {
		p = p[i+1:]
	}
	p = strings.TrimSuffix(p, "{$}")
	if p == "" {
		return otherRoute
	}
	if p != "/" {
		p = strings.TrimSuffix(p, "/")
	}
	if p == "/" && r.URL.Path != "/" {
		return otherRoute
	}

	return p
}

type statusWriter struct {
	http.ResponseWriter

	status int
}

func (w *statusWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}
