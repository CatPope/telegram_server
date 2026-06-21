package ratelimit

import (
	"context"
	"errors"
	"sync"
	"time"
)

var ErrLimited = errors.New("ratelimit: limited")

type Decision struct {
	Allowed    bool
	RetryAfter time.Duration
}

type RateLimiter interface {
	Allow(ctx context.Context, key string) (Decision, error)
}

type Policy struct {
	RatePerSec float64
	Burst      float64
}

type tokenBucket struct {
	tokens     float64
	lastRefill time.Time
}

type bucketLimiter struct {
	policyFn func(key string) Policy
	now      func() time.Time
	mu       sync.Mutex
	buckets  map[string]*tokenBucket
}

func newBucketLimiter(policyFn func(string) Policy, now func() time.Time) *bucketLimiter {
	if now == nil {
		now = time.Now
	}
	return &bucketLimiter{
		policyFn: policyFn,
		now:      now,
		buckets:  make(map[string]*tokenBucket),
	}
}

func (l *bucketLimiter) Allow(_ context.Context, key string) (Decision, error) {
	pol := l.policyFn(key)
	if pol.RatePerSec <= 0 || pol.Burst <= 0 {
		return Decision{Allowed: true}, nil
	}
	now := l.now()
	l.mu.Lock()
	defer l.mu.Unlock()
	b, ok := l.buckets[key]
	if !ok {
		b = &tokenBucket{tokens: pol.Burst, lastRefill: now}
		l.buckets[key] = b
	}
	elapsed := now.Sub(b.lastRefill).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * pol.RatePerSec
		if b.tokens > pol.Burst {
			b.tokens = pol.Burst
		}
		b.lastRefill = now
	}
	if b.tokens >= 1 {
		b.tokens -= 1
		return Decision{Allowed: true}, nil
	}
	wait := time.Duration((1 - b.tokens) / pol.RatePerSec * float64(time.Second))
	return Decision{Allowed: false, RetryAfter: wait}, nil
}
