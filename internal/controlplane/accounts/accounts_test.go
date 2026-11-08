package accounts_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/St1viG/cj/internal/controlplane/accounts"
	"github.com/St1viG/cj/internal/store/db"
	"github.com/St1viG/cj/internal/store/storetest"
)

func TestPasswords(t *testing.T) {
	h := accounts.HashPassword("correct horse")
	if !strings.HasPrefix(h, "$argon2id$v=19$") || h == accounts.HashPassword("correct horse") {
		t.Fatalf("hash %q: want argon2id with a random salt", h)
	}
	if ok, err := accounts.VerifyPassword(h, "correct horse"); !ok || err != nil {
		t.Fatalf("right password = %v, %v", ok, err)
	}
	if ok, err := accounts.VerifyPassword(h, "wrong horse"); ok || err != nil {
		t.Fatalf("wrong password = %v, %v", ok, err)
	}
	for _, bad := range []string{"", "plain", "$bcrypt$x$y$z$w", "$argon2id$v=19$m=1,t=1,p=1$!!$!!"} {
		if _, err := accounts.VerifyPassword(bad, "x"); !errors.Is(err, accounts.ErrBadHash) {
			t.Errorf("VerifyPassword(%q) = %v, want ErrBadHash", bad, err)
		}
	}
}

func TestAPIKeyShape(t *testing.T) {
	secret, prefix, hash := accounts.NewAPIKey()
	if !accounts.LooksLikeAPIKey(secret) || !strings.HasPrefix(secret, prefix) || len(prefix) != 12 || len(hash) != 32 {
		t.Fatalf("secret %q prefix %q hash %d bytes", secret, prefix, len(hash))
	}
	if other, _, _ := accounts.NewAPIKey(); other == secret {
		t.Fatal("two keys are equal")
	}
	if string(accounts.HashSecret(secret)) != string(hash) {
		t.Fatal("HashSecret differs from the stored hash")
	}
	for _, s := range []string{"", "kdk_short", "xyz_" + strings.Repeat("a", 40)} {
		if accounts.LooksLikeAPIKey(s) {
			t.Errorf("LooksLikeAPIKey(%q) = true", s)
		}
	}
}

func TestSeedDemoIsIdempotent(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	if err := s.UpsertRegion(ctx, db.UpsertRegionParams{Name: accounts.DemoRegion}); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.SeedDemo(ctx, s, "short"); err == nil {
		t.Fatal("a 5-character password was accepted")
	}

	first, err := accounts.SeedDemo(ctx, s, "demo-password")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 3 {
		t.Fatalf("seeded %d users, want 3", len(first))
	}
	roles := map[string]string{}
	for _, u := range first {
		if u.APIKey == "" {
			t.Fatalf("%s got no API key on the first run", u.Email)
		}
		row, err := s.GetPrincipalByAPIKeyHash(ctx, accounts.HashSecret(u.APIKey))
		if err != nil || row.User.Email != u.Email || row.Org.Name != u.Org {
			t.Fatalf("key of %s resolves to %+v, %v", u.Email, row, err)
		}
		roles[u.Email] = row.Org.Kind
		user, _ := s.GetUserByEmail(ctx, u.Email)
		if ok, err := accounts.VerifyPassword(user.PasswordHash, "demo-password"); !ok || err != nil {
			t.Fatalf("password of %s does not verify: %v", u.Email, err)
		}
	}
	if roles[accounts.CustomerUser] != "customer" || roles[accounts.PartnerUser] != "partner" || roles[accounts.OperatorUser] != "operator" {
		t.Fatalf("roles = %v", roles)
	}

	second, err := accounts.SeedDemo(ctx, s, "demo-password")
	if err != nil {
		t.Fatal(err)
	}
	for _, u := range second {
		if u.APIKey != "" {
			t.Fatalf("second run created a key for %s", u.Email)
		}
	}
	var keys, users, orgs, sites int
	if err := s.Pool.QueryRow(ctx, `SELECT (SELECT count(*) FROM api_keys), (SELECT count(*) FROM users),
		(SELECT count(*) FROM orgs), (SELECT count(*) FROM sites)`).Scan(&keys, &users, &orgs, &sites); err != nil {
		t.Fatal(err)
	}
	if keys != 3 || users != 3 || orgs != 3 || sites != 1 {
		t.Fatalf("after two runs: %d keys, %d users, %d orgs, %d sites; want 3, 3, 3, 1", keys, users, orgs, sites)
	}
}
