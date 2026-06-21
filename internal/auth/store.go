package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	ErrBearerMalformed = errors.New("auth: bearer token malformed")
	ErrKeyNotFound     = errors.New("auth: key not found")
)

const bearerPrefix = "tg_"

type KeyStore struct {
	pool *pgxpool.Pool
}

func NewKeyStore(pool *pgxpool.Pool) *KeyStore {
	return &KeyStore{pool: pool}
}

func ParseBearer(token string) (keyPrefix string, err error) {
	if !strings.HasPrefix(token, bearerPrefix) {
		return "", ErrBearerMalformed
	}
	rest := token[len(bearerPrefix):]
	idx := strings.IndexByte(rest, '_')
	if idx <= 0 || idx >= len(rest)-1 {
		return "", ErrBearerMalformed
	}
	return rest[:idx], nil
}

func (s *KeyStore) Resolve(ctx context.Context, bearer string) (RequesterIdentity, error) {
	prefix, err := ParseBearer(bearer)
	if err != nil {
		return RequesterIdentity{}, err
	}
	const q = `
		SELECT k.app_id, k.key_hash
		FROM app_keys k
		JOIN apps a ON a.id = k.app_id
		WHERE k.key_prefix = $1 AND k.revoked_at IS NULL AND a.active = true`
	rows, err := s.pool.Query(ctx, q, prefix)
	if err != nil {
		return RequesterIdentity{}, fmt.Errorf("auth: query key: %w", err)
	}
	defer rows.Close()
	var appID, hash string
	matched := false
	for rows.Next() {
		var rowAppID, rowHash string
		if scanErr := rows.Scan(&rowAppID, &rowHash); scanErr != nil {
			return RequesterIdentity{}, fmt.Errorf("auth: scan: %w", scanErr)
		}
		ok, vErr := VerifyAPIKey(bearer, rowHash)
		if vErr != nil {
			continue
		}
		if ok {
			appID = rowAppID
			hash = rowHash
			matched = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return RequesterIdentity{}, fmt.Errorf("auth: rows: %w", err)
	}
	if !matched {
		return RequesterIdentity{}, ErrKeyNotFound
	}
	caps, err := s.loadCapabilities(ctx, appID)
	if err != nil {
		return RequesterIdentity{}, err
	}
	_ = hash
	return RequesterIdentity{
		AppID:        appID,
		Capabilities: caps,
		KeyPrefix:    prefix,
	}, nil
}

func (s *KeyStore) loadCapabilities(ctx context.Context, appID string) (CapabilitySet, error) {
	rows, err := s.pool.Query(ctx, `SELECT capability FROM app_capabilities WHERE app_id = $1`, appID)
	if err != nil {
		return nil, fmt.Errorf("auth: query caps: %w", err)
	}
	defer rows.Close()
	set := NewCapabilitySet()
	for rows.Next() {
		var c string
		if scanErr := rows.Scan(&c); scanErr != nil {
			return nil, fmt.Errorf("auth: scan cap: %w", scanErr)
		}
		set.Add(Capability(c))
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("auth: rows: %w", err)
	}
	return set, nil
}

var _ = pgx.ErrNoRows
