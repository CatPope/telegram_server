package audit

import (
	"bytes"
	"crypto/sha256"
	"time"
)

// genesisSeed anchors the hash chain: the first audit_log row's prev_hash
// is SHA-256 of this string. Changing it invalidates every existing chain —
// never touch it without a migration that recomputes all hashes.
const genesisSeed = "telegram_server-audit-genesis-v1"

// ChainLockKey is the pg_advisory_xact_lock key that serializes audit_log
// inserts so each row can link to the committed head of the chain. The
// value is "auditcha" read as a big-endian int64 — arbitrary but must be
// unique among advisory lock users of this database.
const ChainLockKey int64 = 0x6175646974636861

// GenesisHash returns the chain anchor: SHA-256(genesisSeed).
func GenesisHash() []byte {
	h := sha256.Sum256([]byte(genesisSeed))
	return h[:]
}

// ComputeRowHash chains one row: SHA-256(prev ‖ payload). payload is the
// canonical row serialization produced by the SQL function
// audit_chain_payload (migrations/0007) — that function is the single
// implementation of the field-order/NULL/escaping rules; Go never rebuilds
// the payload from column values.
func ComputeRowHash(prev, payload []byte) []byte {
	h := sha256.New()
	h.Write(prev)
	h.Write(payload)
	return h.Sum(nil)
}

// ChainRow is one audit_log row as the verifier consumes it: the stored
// hashes plus the payload recomputed by audit_chain_payload over the
// stored column values.
type ChainRow struct {
	ID       int64
	At       time.Time
	Stage    string
	PrevHash []byte
	RowHash  []byte
	Payload  []byte
}

// VerifyBreak identifies the first row whose chain check failed.
type VerifyBreak struct {
	ID       int64
	At       time.Time
	Stage    string
	Column   string // "prev_hash" or "row_hash"
	Expected []byte
	Stored   []byte
}

// VerifyResult is the outcome of a chain walk. Rows counts rows that
// verified clean — on a break or an iterator error it is the progress
// reached, not the table size.
type VerifyResult struct {
	OK    bool
	Rows  int64
	Break *VerifyBreak
}

// VerifyChain walks the chain via next, which must yield rows in
// id-ascending order and report ok=false at the end. Only the previous
// row's hash is retained, so the walk streams in constant memory. When
// next returns an error (e.g. a query deadline), the result carries the
// progress so far and the error is returned for the caller to classify.
func VerifyChain(next func() (ChainRow, bool, error)) (VerifyResult, error) {
	running := GenesisHash()
	var res VerifyResult
	for {
		row, ok, err := next()
		if err != nil {
			return res, err
		}
		if !ok {
			res.OK = true
			return res, nil
		}
		if !bytes.Equal(row.PrevHash, running) {
			res.Break = &VerifyBreak{
				ID: row.ID, At: row.At, Stage: row.Stage,
				Column: "prev_hash", Expected: running, Stored: row.PrevHash,
			}
			return res, nil
		}
		want := ComputeRowHash(running, row.Payload)
		if !bytes.Equal(row.RowHash, want) {
			res.Break = &VerifyBreak{
				ID: row.ID, At: row.At, Stage: row.Stage,
				Column: "row_hash", Expected: want, Stored: row.RowHash,
			}
			return res, nil
		}
		running = row.RowHash
		res.Rows++
	}
}
