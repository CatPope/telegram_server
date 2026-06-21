package ratelimit

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

// PolicyLoader loads per-target rate-limit policies from the database for a
// given scope (e.g. "request", "dispatch_chat").
type PolicyLoader struct {
	pool  *pgxpool.Pool
	scope string
}

// NewPolicyLoader creates a PolicyLoader for the given scope.
func NewPolicyLoader(pool *pgxpool.Pool, scope string) *PolicyLoader {
	return &PolicyLoader{pool: pool, scope: scope}
}

// Load fetches all rows for the loader's scope and returns a map of
// target → Policy.
func (pl *PolicyLoader) Load(ctx context.Context) (map[string]Policy, error) {
	const q = `SELECT target, rate_per_sec, burst FROM rate_limit_policies WHERE scope = $1`
	rows, err := pl.pool.Query(ctx, q, pl.scope)
	if err != nil {
		return nil, fmt.Errorf("ratelimit: load policies (scope=%s): %w", pl.scope, err)
	}
	defer rows.Close()

	overrides := make(map[string]Policy)
	for rows.Next() {
		var target string
		var ratePerSec, burst int
		if scanErr := rows.Scan(&target, &ratePerSec, &burst); scanErr != nil {
			return nil, fmt.Errorf("ratelimit: scan policy: %w", scanErr)
		}
		overrides[target] = Policy{
			RatePerSec: float64(ratePerSec),
			Burst:      float64(burst),
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("ratelimit: rows: %w", err)
	}
	return overrides, nil
}

// BuildRequestLimiter loads policies from the DB and returns a RequestLimiter
// using defaultPolicy for any key not present in the DB.
func (pl *PolicyLoader) BuildRequestLimiter(ctx context.Context, defaultPolicy Policy) (*RequestLimiter, error) {
	overrides, err := pl.Load(ctx)
	if err != nil {
		return nil, err
	}
	return NewRequestLimiter(defaultPolicy, overrides), nil
}

// Reload fetches the latest policies and returns the updated override map.
// Callers that need hot-swap can call this after capability mutations and
// rebuild their limiter. Full in-place hot-swap is out of scope for Phase 4;
// this stub satisfies the interface contract.
func (pl *PolicyLoader) Reload(ctx context.Context) (map[string]Policy, error) {
	return pl.Load(ctx)
}
