package esi

import (
	"fmt"
	"math"
	"sync"
	"time"
)

const (
	UserBucketCapacity = 400.0
	UserBucketRefill   = 2.0
)

// UserLimitedError is this user's ESI request allowance. Do not retry
// before RetryAt — looping would burn the shared CCP error-limit.
type UserLimitedError struct {
	Msg      string
	RetryAt  time.Time
	RetrySec int
}

func (e UserLimitedError) Error() string { return e.Msg }

type userBucket struct {
	mu     sync.Mutex
	tokens float64
	last   time.Time
}

func newUserBucket() *userBucket {
	return &userBucket{tokens: UserBucketCapacity}
}

func (b *userBucket) take() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	b.refillLocked(now)
	if b.tokens < 1 {
		deficit := 1 - b.tokens
		wait := deficit / UserBucketRefill
		retrySec := max(int(math.Ceil(wait)), 1)
		retryAt := now.Add(time.Duration(wait * float64(time.Second)))

		return UserLimitedError{
			Msg: fmt.Sprintf(
				"This user's ESI request allowance is spent (refills at %.0f/s). Wait until %s, then call the same tool once. Do not retry in a loop.",
				UserBucketRefill, retryAt.UTC().Format(time.RFC3339),
			),
			RetryAt:  retryAt,
			RetrySec: retrySec,
		}
	}
	b.tokens--

	return nil
}

func (b *userBucket) refund() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.tokens++
	if b.tokens > UserBucketCapacity {
		b.tokens = UserBucketCapacity
	}
}

func (b *userBucket) remaining() float64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.refillLocked(time.Now())

	return b.tokens
}

func (b *userBucket) refillLocked(now time.Time) {
	if b.last.IsZero() {
		b.last = now

		return
	}
	elapsed := now.Sub(b.last).Seconds()
	if elapsed <= 0 {
		return
	}
	b.tokens += elapsed * UserBucketRefill
	if b.tokens > UserBucketCapacity {
		b.tokens = UserBucketCapacity
	}
	b.last = now
}
