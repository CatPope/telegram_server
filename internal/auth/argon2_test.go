package auth

import "testing"

func TestArgon2PinnedConstants(t *testing.T) {
	if Argon2Memory != 64*1024 {
		t.Fatalf("Argon2Memory drift: got %d, want %d", Argon2Memory, 64*1024)
	}
	if Argon2Iterations != 3 {
		t.Fatalf("Argon2Iterations drift: got %d, want 3", Argon2Iterations)
	}
	if Argon2Parallelism != 1 {
		t.Fatalf("Argon2Parallelism drift: got %d, want 1", Argon2Parallelism)
	}
	if Argon2KeyLen != 32 {
		t.Fatalf("Argon2KeyLen drift: got %d, want 32", Argon2KeyLen)
	}
}

func TestHashAndVerifyRoundtrip(t *testing.T) {
	encoded, err := HashAPIKey("tg_test_abc")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, err := VerifyAPIKey("tg_test_abc", encoded)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("verify returned false for correct key")
	}
}

func TestVerifyRejectsWrongKey(t *testing.T) {
	encoded, err := HashAPIKey("tg_test_abc")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	ok, err := VerifyAPIKey("tg_test_xyz", encoded)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ok {
		t.Fatal("verify accepted wrong key")
	}
}

func TestVerifyRejectsMalformedHash(t *testing.T) {
	cases := []string{
		"",
		"not-argon",
		"$argon2id$v=19$m=64,t=3,p=1$bad",
		"$argon2id$v=18$m=64,t=3,p=1$AAAA$AAAA",
	}
	for _, c := range cases {
		if _, err := VerifyAPIKey("x", c); err == nil {
			t.Errorf("expected error for %q, got nil", c)
		}
	}
}

func TestVerifyRejectsWeakenedParams(t *testing.T) {
	weakened := "$argon2id$v=19$m=1024,t=1,p=1$AAAAAAAAAAAAAAAA$AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"
	if _, err := VerifyAPIKey("x", weakened); err != ErrUnsupportedParams {
		t.Fatalf("expected ErrUnsupportedParams, got %v", err)
	}
}

func TestParseBearer(t *testing.T) {
	prefix, err := ParseBearer("tg_devadmin_xyzzy")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if prefix != "devadmin" {
		t.Fatalf("prefix mismatch: %s", prefix)
	}
	if _, err := ParseBearer("invalid"); err == nil {
		t.Fatal("expected error for non-prefixed bearer")
	}
	if _, err := ParseBearer("tg_"); err == nil {
		t.Fatal("expected error for missing prefix segment")
	}
	if _, err := ParseBearer("tg_devadmin_"); err == nil {
		t.Fatal("expected error for missing secret segment")
	}
}
