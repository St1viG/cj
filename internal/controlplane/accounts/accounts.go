// Package accounts holds the demo principals (ADR-0021): API-key and
// password credentials, and the seeded orgs and users.
package accounts

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"

	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// APIKeyPrefix starts every API key secret.
const APIKeyPrefix = "kdk_"

// prefixLen is how much of the secret is stored in clear to recognize it.
const prefixLen = len(APIKeyPrefix) + 8

var keyEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// NewAPIKey returns a fresh secret, its displayable prefix, and the hash
// that is stored.
func NewAPIKey() (secret, prefix string, hash []byte) {
	b := make([]byte, 25)
	_, _ = rand.Read(b) // crypto/rand.Read never fails
	secret = APIKeyPrefix + strings.ToLower(keyEncoding.EncodeToString(b))
	return secret, secret[:prefixLen], HashSecret(secret)
}

// HashSecret is the stored form of an API key secret. A plain SHA-256 is
// enough: the secret has 200 random bits.
func HashSecret(secret string) []byte {
	sum := sha256.Sum256([]byte(secret))
	return sum[:]
}

// LooksLikeAPIKey reports whether s has the shape of an API key secret.
func LooksLikeAPIKey(s string) bool {
	return strings.HasPrefix(s, APIKeyPrefix) && len(s) == len(APIKeyPrefix)+40
}

// argon2id parameters (RFC 9106's second recommended option, smaller memory).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024
	argonThreads = 2
	argonKeyLen  = 32
)

// HashPassword returns a PHC-formatted argon2id hash.
func HashPassword(password string) string {
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

// ErrBadHash means a stored password hash cannot be parsed.
var ErrBadHash = errors.New("accounts: unrecognized password hash")

// VerifyPassword checks password against a HashPassword result.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false, ErrBadHash
	}
	var (
		version         int
		memory, time    uint32
		threads         uint8
		salt, want, got []byte
		err             error
	)
	if _, err = fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false, ErrBadHash
	}
	if _, err = fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false, ErrBadHash
	}
	if salt, err = base64.RawStdEncoding.DecodeString(parts[4]); err != nil {
		return false, ErrBadHash
	}
	if want, err = base64.RawStdEncoding.DecodeString(parts[5]); err != nil || len(want) == 0 {
		return false, ErrBadHash
	}
	got = argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want))) //nolint:gosec // len(want) is a stored hash length
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

// Demo account names (ADR-0021, roadmap P3.8).
const (
	DemoPartner  = "demo-dc"
	DemoSite     = "demo"
	DemoRegion   = "demo"
	CustomerOrg  = "acme"
	OperatorOrg  = "kondukter"
	CustomerUser = "customer@demo.local"
	PartnerUser  = "partner@demo.local"
	OperatorUser = "operator@demo.local"
)

// Seeded is one seeded user.
type Seeded struct {
	Email string
	Org   string
	Role  string
	// APIKey is set only when this run created the user's key; it is not
	// stored anywhere and cannot be shown again.
	APIKey string
}

// SeedDemo creates or updates the demo partner and site, the customer,
// partner and operator orgs, one user in each with password, and an API
// key for every user that has none. It is idempotent: a second run creates
// no keys. The region must exist (`kondukter seed catalog`).
func SeedDemo(ctx context.Context, s *store.Store, password string) ([]Seeded, error) {
	if len(password) < 8 {
		return nil, errors.New("accounts: the seed password must be at least 8 characters")
	}
	var out []Seeded
	err := s.WithTx(ctx, func(tx store.Tx) error {
		out = nil
		partner, err := tx.UpsertPartner(ctx, db.UpsertPartnerParams{Name: DemoPartner, LegalName: "Demo Datacenter", ContactEmail: PartnerUser})
		if err != nil {
			return err
		}
		if _, err := tx.UpsertSite(ctx, db.UpsertSiteParams{PartnerID: partner.ID, Name: DemoSite, Region: DemoRegion}); err != nil {
			return fmt.Errorf("site %s (is region %q seeded? run `kondukter seed catalog`): %w", DemoSite, DemoRegion, err)
		}
		users := []struct {
			email, display, org, kind string
			partner                   bool
		}{
			{CustomerUser, "Demo Customer", CustomerOrg, "customer", false},
			{PartnerUser, "Demo Partner", DemoPartner, "partner", true},
			{OperatorUser, "Demo Operator", OperatorOrg, "operator", false},
		}
		for _, u := range users {
			params := db.UpsertOrgParams{Name: u.org, Kind: u.kind}
			if u.partner {
				params.PartnerID = &partner.ID
			}
			org, err := tx.UpsertOrg(ctx, params)
			if err != nil {
				return err
			}
			user, err := tx.UpsertUser(ctx, db.UpsertUserParams{Email: u.email, DisplayName: u.display, PasswordHash: HashPassword(password)})
			if err != nil {
				return err
			}
			if err := tx.AddMembership(ctx, db.AddMembershipParams{OrgID: org.ID, UserID: user.ID}); err != nil {
				return err
			}
			seeded := Seeded{Email: u.email, Org: u.org, Role: u.kind}
			active, err := tx.CountActiveAPIKeys(ctx, db.CountActiveAPIKeysParams{OrgID: org.ID, UserID: user.ID})
			if err != nil {
				return err
			}
			if active == 0 {
				secret, prefix, hash := NewAPIKey()
				if _, err := tx.CreateAPIKey(ctx, db.CreateAPIKeyParams{
					OrgID: org.ID, UserID: user.ID, Name: "seed", Prefix: prefix, SecretHash: hash,
				}); err != nil {
					return err
				}
				seeded.APIKey = secret
			}
			out = append(out, seeded)
		}
		return nil
	})
	return out, err
}
