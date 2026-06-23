package telegram

import (
	"context"
	"sync"
	"time"

	"github.com/CatPope/telegram_server/internal/ratelimit"
)

const (
	defaultGlobalRatePerSec  = 25.0
	defaultPerChatRatePerSec = 1.0
	defaultBurstGlobal       = 30.0
	defaultBurstPerChat      = 2.0
)

type DispatchLimiter struct {
	now func() time.Time

	muGlobal sync.Mutex
	gTokens  float64
	gRefill  time.Time
	gRate    float64
	gBurst   float64

	muPer  sync.Mutex
	per    map[string]*perChatBucket
	pRate  float64
	pBurst float64
}

type perChatBucket struct {
	tokens float64
	refill time.Time
}

func NewDispatchLimiter() *DispatchLimiter {
	now := time.Now()
	return &DispatchLimiter{
		now:     time.Now,
		gTokens: defaultBurstGlobal,
		gRefill: now,
		gRate:   defaultGlobalRatePerSec,
		gBurst:  defaultBurstGlobal,
		per:     make(map[string]*perChatBucket),
		pRate:   defaultPerChatRatePerSec,
		pBurst:  defaultBurstPerChat,
	}
}

func (l *DispatchLimiter) Allow(_ context.Context, key string) (ratelimit.Decision, error) {
	now := l.now()
	if d, ok := l.takeGlobal(now); !ok {
		return d, nil
	}
	return l.takePerChat(key, now), nil
}

func (l *DispatchLimiter) takeGlobal(now time.Time) (ratelimit.Decision, bool) {
	l.muGlobal.Lock()
	defer l.muGlobal.Unlock()
	elapsed := now.Sub(l.gRefill).Seconds()
	if elapsed > 0 {
		l.gTokens += elapsed * l.gRate
		if l.gTokens > l.gBurst {
			l.gTokens = l.gBurst
		}
		l.gRefill = now
	}
	if l.gTokens >= 1 {
		l.gTokens -= 1
		return ratelimit.Decision{Allowed: true}, true
	}
	wait := time.Duration((1 - l.gTokens) / l.gRate * float64(time.Second))
	return ratelimit.Decision{Allowed: false, RetryAfter: wait}, false
}

func (l *DispatchLimiter) takePerChat(key string, now time.Time) ratelimit.Decision {
	l.muPer.Lock()
	defer l.muPer.Unlock()
	b, ok := l.per[key]
	if !ok {
		b = &perChatBucket{tokens: l.pBurst, refill: now}
		l.per[key] = b
	}
	elapsed := now.Sub(b.refill).Seconds()
	if elapsed > 0 {
		b.tokens += elapsed * l.pRate
		if b.tokens > l.pBurst {
			b.tokens = l.pBurst
		}
		b.refill = now
	}
	if b.tokens >= 1 {
		b.tokens -= 1
		return ratelimit.Decision{Allowed: true}
	}
	wait := time.Duration((1 - b.tokens) / l.pRate * float64(time.Second))
	return ratelimit.Decision{Allowed: false, RetryAfter: wait}
}
