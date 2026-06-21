package ratelimit

import "time"

type RequestLimiter struct{ *bucketLimiter }

func NewRequestLimiter(defaultPolicy Policy, overrides map[string]Policy) *RequestLimiter {
	pf := func(key string) Policy {
		if p, ok := overrides[key]; ok {
			return p
		}
		return defaultPolicy
	}
	return &RequestLimiter{bucketLimiter: newBucketLimiter(pf, time.Now)}
}
