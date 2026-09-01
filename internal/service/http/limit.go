package httpsvc

import (
	"net/http"
	"strconv"
	"sync"
	"time"

	"github.com/truewebber/gopkg/log"

	"github.com/truewebber/eve-online-mcp/internal/usecase/oauth"
)

const (
	publicRateLimit  = 60
	publicRateWindow = time.Minute
)

type limiter struct {
	mu     sync.Mutex
	hits   map[string][]time.Time
	logger log.Logger
}

func newLimiter(logger log.Logger) *limiter {
	return &limiter{hits: map[string][]time.Time{}, logger: logger}
}

func (l *limiter) wrap(next http.Handler, trustConnectingIP bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ip := oauth.ClientIP(r, trustConnectingIP)
		retry, ok := l.allow(ip)
		if ok {
			next.ServeHTTP(w, r)

			return
		}
		l.logger.Info("http: rate limited", "ip", ip, "retry_after_sec", int(retry.Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(int(retry.Seconds())))
		http.Error(w, "rate limited", http.StatusTooManyRequests)
	})
}

func (l *limiter) allow(ip string) (time.Duration, bool) {
	now := time.Now()
	window := now.Add(-publicRateWindow)
	l.mu.Lock()
	defer l.mu.Unlock()
	kept := l.hits[ip][:0]
	for _, at := range l.hits[ip] {
		if at.After(window) {
			kept = append(kept, at)
		}
	}
	if len(kept) >= publicRateLimit {
		retry := max(publicRateWindow-now.Sub(kept[0]), time.Second)
		l.hits[ip] = kept

		return retry, false
	}
	l.hits[ip] = append(kept, now)

	return 0, true
}
