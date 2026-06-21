package ratelimit

import (
	"context"
	"testing"
	"time"
)

func TestBucketLimiterAllowsUpToBurst(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	rl := newBucketLimiter(
		func(string) Policy { return Policy{RatePerSec: 1, Burst: 3} },
		func() time.Time { return now },
	)
	for i := 0; i < 3; i++ {
		d, err := rl.Allow(context.Background(), "k")
		if err != nil || !d.Allowed {
			t.Fatalf("expected allow on iter %d, got %+v %v", i, d, err)
		}
	}
	d, err := rl.Allow(context.Background(), "k")
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if d.Allowed {
		t.Fatal("expected deny after burst exhausted")
	}
	if d.RetryAfter <= 0 {
		t.Fatalf("expected positive retry-after, got %v", d.RetryAfter)
	}
}

func TestBucketLimiterRefillsOverTime(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	rl := newBucketLimiter(
		func(string) Policy { return Policy{RatePerSec: 10, Burst: 1} },
		func() time.Time { return now },
	)
	d, _ := rl.Allow(context.Background(), "k")
	if !d.Allowed {
		t.Fatal("expected first allow")
	}
	d, _ = rl.Allow(context.Background(), "k")
	if d.Allowed {
		t.Fatal("expected deny right after burst")
	}
	now = now.Add(200 * time.Millisecond)
	d, _ = rl.Allow(context.Background(), "k")
	if !d.Allowed {
		t.Fatalf("expected allow after refill: %+v", d)
	}
}

func TestRequestLimiterUsesOverridePolicy(t *testing.T) {
	rl := NewRequestLimiter(
		Policy{RatePerSec: 1000, Burst: 1000},
		map[string]Policy{"app:slow": {RatePerSec: 1, Burst: 1}},
	)
	d, _ := rl.Allow(context.Background(), "app:slow")
	if !d.Allowed {
		t.Fatal("first call should pass")
	}
	d, _ = rl.Allow(context.Background(), "app:slow")
	if d.Allowed {
		t.Fatal("second call within burst window should be limited")
	}
}

func TestZeroPolicyAllowsAll(t *testing.T) {
	rl := NewRequestLimiter(Policy{}, nil)
	for i := 0; i < 1000; i++ {
		d, err := rl.Allow(context.Background(), "any")
		if err != nil || !d.Allowed {
			t.Fatalf("expected unlimited on iter %d", i)
		}
	}
}
