package auth

import "testing"

// Locks dev-seed hashes (migrations/0002_seed_dev.up.sql) to their cleartexts.
// If anyone regenerates either side without the other, this fails.
func TestDevSeedHashesVerifyAgainstSeed(t *testing.T) {
	cases := []struct {
		bearer string
		hash   string
	}{
		{
			"tg_devadmin_0123456789abcdef0123456789abcdef",
			"$argon2id$v=19$m=65536,t=3,p=1$EzPcQncHYY0K6t1zDjsbrA$wKmkiRtfOPfurozkMKNfQeHAn25otgMlj7isuawi/88",
		},
		{
			"tg_devdev_0123456789abcdef0123456789abcdef",
			"$argon2id$v=19$m=65536,t=3,p=1$PT+GtkiVFrPc5ukoCtwaVQ$pvxEZZpnHM4wNIaJ3AbwpfY7vHV9TGQ9tE+kkzIauo0",
		},
		{
			"tg_devuser_0123456789abcdef0123456789abcdef",
			"$argon2id$v=19$m=65536,t=3,p=1$l/Ib30v01+MIrF1sN4yVIQ$XwfMbQH7q316NO1K7zgmqgfxLot6jhXHBdd2HAZ/RRM",
		},
	}
	for _, tc := range cases {
		ok, err := VerifyAPIKey(tc.bearer, tc.hash)
		if err != nil {
			t.Errorf("verify error for %s: %v", tc.bearer, err)
			continue
		}
		if !ok {
			t.Errorf("hash mismatch for %s", tc.bearer)
		}
	}
}
