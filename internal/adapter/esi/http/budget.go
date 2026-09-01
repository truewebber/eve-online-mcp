package http

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/truewebber/eve-online-mcp/internal/adapter/esi"
)

const (
	errorBudgetCap    = 20
	errorBudgetShare  = 5
	errorBudgetWindow = 60 * time.Second
)

// ErrorBudgetRejections is the SPEC §11 counter for the §5.3 refusal.
var ErrorBudgetRejections atomic.Int64 //nolint:gochecknoglobals // SPEC §11: increment next to the refusal.

type errorBudget struct {
	mu     sync.Mutex
	stamps []time.Time
}

func newErrorBudget() *errorBudget {
	return &errorBudget{}
}

func errorBudgetLimit(remain int) int {
	if remain < 0 {
		remain = 0
	}

	return min(errorBudgetCap, remain/errorBudgetShare)
}

func (b *errorBudget) check(remain int) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.pruneLocked(now)
	limit := errorBudgetLimit(remain)
	if len(b.stamps) < limit {
		return nil
	}
	ErrorBudgetRejections.Add(1)
	retryAt := now.Add(errorBudgetWindow)
	if len(b.stamps) > 0 {
		retryAt = b.stamps[0].Add(errorBudgetWindow)
	}
	wait := max(time.Until(retryAt), time.Second)
	retrySec := max(int(math.Ceil(wait.Seconds())), 1)

	return esi.UserLimitedError{
		Msg: fmt.Sprintf(
			"This character's ESI error budget is spent (%d errors in the last 60s; the shared CCP remainder is %d). Wait until %s, then call the same tool once. Do not retry in a loop.",
			len(b.stamps), remain, retryAt.UTC().Format(time.RFC3339),
		),
		RetryAt:  retryAt,
		RetrySec: retrySec,
		Reason:   esi.ErrBudgetSpent,
	}
}

func (b *errorBudget) charge() {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.pruneLocked(now)
	b.stamps = append(b.stamps, now)
}

func (b *errorBudget) count() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pruneLocked(time.Now())

	return len(b.stamps)
}

func (b *errorBudget) pruneLocked(now time.Time) {
	cutoff := now.Add(-errorBudgetWindow)
	i := 0
	for i < len(b.stamps) && !b.stamps[i].After(cutoff) {
		i++
	}
	if i > 0 {
		b.stamps = append([]time.Time(nil), b.stamps[i:]...)
	}
}
