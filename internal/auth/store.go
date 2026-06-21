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

	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{
		AccessMode: pgx.ReadOnly,
		IsoLevel:   pgx.RepeatableRead,
	})
	if err != nil {
		return RequesterIdentity{}, fmt.Errorf("auth: begin: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	const q = `
		SELECT k.app_id, k.key_hash, a.capability_set_version
		FROM app_keys k
		JOIN apps a ON a.id = k.app_id
		WHERE k.key_prefix = $1 AND k.revoked_at IS NULL AND a.active = true`
	rows, err := tx.Query(ctx, q, prefix)
	if err != nil {
		return RequesterIdentity{}, fmt.Errorf("auth: query key: %w", err)
	}
	defer rows.Close()
	var appID string
	var capVer int64
	matched := false
	for rows.Next() {
		var rowAppID, rowHash string
		var rowCapVer int64
		if scanErr := rows.Scan(&rowAppID, &rowHash, &rowCapVer); scanErr != nil {
			return RequesterIdentity{}, fmt.Errorf("auth: scan: %w", scanErr)
		}
		ok, vErr := VerifyAPIKey(bearer, rowHash)
		if vErr != nil {
			continue
		}
		if ok {
			appID = rowAppID
			capVer = rowCapVer
			matched = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return RequesterIdentity{}, fmt.Errorf("auth: rows: %w", err)
	}
	// pgx allows only one active query per tx. Close the key-lookup cursor
	// before starting the capability-load query on the same tx.
	rows.Close()
	if !matched {
		return RequesterIdentity{}, ErrKeyNotFound
	}
	caps, err := loadCapabilitiesTx(ctx, tx, appID)
	if err != nil {
		return RequesterIdentity{}, err
	}
	if err := tx.Commit(ctx); err != nil {
		return RequesterIdentity{}, fmt.Errorf("auth: commit: %w", err)
	}
	return RequesterIdentity{
		AppID:            appID,
		Capabilities:     caps,
		CapabilitySetVer: capVer,
		KeyPrefix:        prefix,
	}, nil
}

func loadCapabilitiesTx(ctx context.Context, tx pgx.Tx, appID string) (CapabilitySet, error) {
	rows, err := tx.Query(ctx, `SELECT capability FROM app_capabilities WHERE app_id = $1`, appID)
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
