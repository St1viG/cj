package pki_test

import (
	"context"
	"errors"
	"testing"

	"github.com/St1viG/cj/internal/controlplane/pki"
	"github.com/St1viG/cj/internal/store/storetest"
)

func TestLoadOrCreateIsStableAndSealed(t *testing.T) {
	ctx := context.Background()
	s := storetest.New(t)
	key := "correct horse battery staple"

	a, err := pki.LoadOrCreate(ctx, s, key)
	if err != nil {
		t.Fatal(err)
	}
	b, err := pki.LoadOrCreate(ctx, s, key)
	if err != nil {
		t.Fatal(err)
	}
	if string(a.CertPEM()) != string(b.CertPEM()) {
		t.Fatal("a second start created a different CA")
	}
	if _, err := pki.LoadOrCreate(ctx, s, "the wrong master key!!"); !errors.Is(err, pki.ErrMasterKey) {
		t.Fatalf("wrong master key: err = %v", err)
	}
	if _, err := pki.LoadOrCreate(ctx, s, "short"); err == nil {
		t.Fatal("a short master key was accepted")
	}
	row, err := s.GetCA(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(row.KeyCiphertext) < 100 {
		t.Fatalf("stored key looks unsealed: %d bytes", len(row.KeyCiphertext))
	}
}
