package audit

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"
	"time"
)

// TestGenesisHashPinned is the regression anchor for the chain seed. If
// this fails, every deployed chain is invalid — do not "fix" the test.
func TestGenesisHashPinned(t *testing.T) {
	const want = "5bfca120522968e0bb1cebbb87a2b656d5c57639b92f17dbb909c76a0299344d"
	if got := hex.EncodeToString(GenesisHash()); got != want {
		t.Fatalf("GenesisHash = %s, want %s", got, want)
	}
}

// TestComputeRowHashPinned pins the chain step against a payload in the
// exact serialization audit_chain_payload (migrations/0007) produces:
// 'v1' + 14 '|'-separated fields, NULL as '\N', details as jsonb text.
func TestComputeRowHashPinned(t *testing.T) {
	payload := []byte(`v1|1767225600.500000|\N|\N|received|ci-notifier|\N|\N|/v1/messages/direct|\N|\N|42|\N|\N|{}`)
	const want = "ef8e5cc1c1e12713601d16ea17e8ee156c20efc8a7c9ee009f6b07b773d53608"
	if got := hex.EncodeToString(ComputeRowHash(GenesisHash(), payload)); got != want {
		t.Fatalf("ComputeRowHash = %s, want %s", got, want)
	}
}

// chainFixture builds an intact chain over the given payloads using the
// same ComputeRowHash the writer/verifier share, so test rows cannot drift
// from the production chaining rule.
func chainFixture(payloads ...string) []ChainRow {
	rows := make([]ChainRow, 0, len(payloads))
	prev := GenesisHash()
	for i, p := range payloads {
		row := ChainRow{
			ID:       int64(i + 1),
			At:       time.Date(2026, 7, 8, 12, 0, i, 0, time.UTC),
			Stage:    "received",
			PrevHash: prev,
			Payload:  []byte(p),
		}
		row.RowHash = ComputeRowHash(prev, row.Payload)
		prev = row.RowHash
		rows = append(rows, row)
	}
	return rows
}

func sliceIter(rows []ChainRow) func() (ChainRow, bool, error) {
	i := 0
	return func() (ChainRow, bool, error) {
		if i >= len(rows) {
			return ChainRow{}, false, nil
		}
		row := rows[i]
		i++
		return row, true, nil
	}
}

func TestVerifyChainIntact(t *testing.T) {
	rows := chainFixture("p1", "p2", "p3")
	res, err := VerifyChain(sliceIter(rows))
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.OK || res.Rows != 3 || res.Break != nil {
		t.Fatalf("expected OK/3 rows, got %+v", res)
	}
}

func TestVerifyChainEmpty(t *testing.T) {
	res, err := VerifyChain(sliceIter(nil))
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if !res.OK || res.Rows != 0 {
		t.Fatalf("expected OK/0 rows, got %+v", res)
	}
}

// A modified column changes the recomputed payload, so the stored row_hash
// no longer matches — the break lands exactly on the tampered row.
func TestVerifyChainDetectsTamperedRow(t *testing.T) {
	rows := chainFixture("p1", "p2", "p3")
	rows[1].Payload = []byte("p2-tampered")

	res, err := VerifyChain(sliceIter(rows))
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if res.OK || res.Break == nil {
		t.Fatalf("expected a break, got %+v", res)
	}
	if res.Break.ID != 2 || res.Break.Column != "row_hash" {
		t.Fatalf("break = %+v, want row 2 / row_hash", res.Break)
	}
	if res.Rows != 1 {
		t.Fatalf("Rows = %d, want 1 (rows verified before the break)", res.Rows)
	}
	if hex.EncodeToString(res.Break.Stored) == hex.EncodeToString(res.Break.Expected) {
		t.Fatal("expected and stored hashes must differ on a break")
	}
}

// An attacker who also rewrites the tampered row's row_hash just moves the
// break to the next row: its prev_hash no longer matches the new head.
func TestVerifyChainDetectsRewrittenHash(t *testing.T) {
	rows := chainFixture("p1", "p2", "p3")
	rows[1].Payload = []byte("p2-tampered")
	rows[1].RowHash = ComputeRowHash(rows[1].PrevHash, rows[1].Payload)

	res, err := VerifyChain(sliceIter(rows))
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if res.OK || res.Break == nil {
		t.Fatalf("expected a break, got %+v", res)
	}
	if res.Break.ID != 3 || res.Break.Column != "prev_hash" {
		t.Fatalf("break = %+v, want row 3 / prev_hash", res.Break)
	}
}

// The first row must chain from the genesis hash, not from zeros or its
// own hash.
func TestVerifyChainGenesisRule(t *testing.T) {
	rows := chainFixture("p1")
	rows[0].PrevHash = make([]byte, 32)

	res, err := VerifyChain(sliceIter(rows))
	if err != nil {
		t.Fatalf("VerifyChain: %v", err)
	}
	if res.OK || res.Break == nil || res.Break.ID != 1 || res.Break.Column != "prev_hash" {
		t.Fatalf("expected genesis break on row 1, got %+v", res)
	}
	if hex.EncodeToString(res.Break.Expected) != hex.EncodeToString(GenesisHash()) {
		t.Fatal("expected hash on the genesis break must be GenesisHash()")
	}
}

// An iterator error (e.g. a query deadline) surfaces with the progress
// reached so far, so callers can report a partial result.
func TestVerifyChainIteratorError(t *testing.T) {
	rows := chainFixture("p1", "p2")
	i := 0
	next := func() (ChainRow, bool, error) {
		if i >= 1 {
			return ChainRow{}, false, context.DeadlineExceeded
		}
		row := rows[i]
		i++
		return row, true, nil
	}

	res, err := VerifyChain(next)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want DeadlineExceeded", err)
	}
	if res.Rows != 1 || res.OK {
		t.Fatalf("expected partial progress of 1 row, got %+v", res)
	}
}
