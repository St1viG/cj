// Package pki is Kondukter's internal certificate authority for node
// identity (SPEC §7.7, ADR-0024).
//
// The CA is created on first start and stored in Postgres with its private
// key sealed (AES-256-GCM) under a key derived from KONDUKTER_MASTER_KEY. It
// signs node client certificates, whose URI SAN kondukter://node/<uuid> is
// the only node identity the gateway trusts, and the gateway's own server
// certificate.
package pki

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/St1viG/cj/internal/agentproto"
	"github.com/St1viG/cj/internal/store"
	"github.com/St1viG/cj/internal/store/db"
)

// NodeCertTTL is the lifetime of a node certificate. Rotation is deferred
// for the demo (ADR-0024), so nodes re-enroll after this.
const NodeCertTTL = 30 * 24 * time.Hour

// ErrMasterKey means the stored CA key cannot be opened with the configured
// master key.
var ErrMasterKey = errors.New("pki: KONDUKTER_MASTER_KEY does not open the stored CA key")

// CA signs certificates.
type CA struct {
	cert    *x509.Certificate
	certPEM []byte
	key     *ecdsa.PrivateKey
	now     func() time.Time
}

// LoadOrCreate returns the stored CA, creating it on first use. masterKey
// must be at least 16 characters.
func LoadOrCreate(ctx context.Context, s *store.Store, masterKey string) (*CA, error) {
	if len(masterKey) < 16 {
		return nil, errors.New("pki: KONDUKTER_MASTER_KEY must be at least 16 characters")
	}
	aead, err := sealer(masterKey)
	if err != nil {
		return nil, err
	}
	row, err := s.GetCA(ctx)
	if errors.Is(store.NotFound(err), store.ErrNotFound) {
		certPEM, keyDER, genErr := newCA()
		if genErr != nil {
			return nil, genErr
		}
		if insErr := s.InsertCA(ctx, db.InsertCAParams{CertPem: string(certPEM), KeyCiphertext: seal(aead, keyDER)}); insErr != nil {
			return nil, fmt.Errorf("pki: store CA: %w", insErr)
		}
		row, err = s.GetCA(ctx) // another starter may have won the insert
	}
	if err != nil {
		return nil, fmt.Errorf("pki: load CA: %w", err)
	}
	keyDER, err := open(aead, row.KeyCiphertext)
	if err != nil {
		return nil, ErrMasterKey
	}
	key, err := x509.ParseECPrivateKey(keyDER)
	if err != nil {
		return nil, fmt.Errorf("pki: CA key: %w", err)
	}
	block, _ := pem.Decode([]byte(row.CertPem))
	if block == nil {
		return nil, errors.New("pki: stored CA certificate is not PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("pki: CA certificate: %w", err)
	}
	return &CA{cert: cert, certPEM: []byte(row.CertPem), key: key, now: time.Now}, nil
}

// CertPEM is the CA certificate, handed to agents at enrollment.
func (ca *CA) CertPEM() []byte { return ca.certPEM }

// Pool is a cert pool holding only this CA.
func (ca *CA) Pool() *x509.CertPool {
	p := x509.NewCertPool()
	p.AddCert(ca.cert)
	return p
}

// SignedNode is a freshly issued node certificate.
type SignedNode struct {
	CertPEM     []byte
	Fingerprint string
	NotAfter    time.Time
}

// SignNode verifies a PEM CSR and issues a client certificate for nodeID.
// Only the CSR's public key is used; subject and SANs come from Kondukter.
func (ca *CA) SignNode(csrPEM []byte, nodeID uuid.UUID, hostname string) (SignedNode, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil || block.Type != "CERTIFICATE REQUEST" {
		return SignedNode{}, errors.New("pki: CSR is not a PEM CERTIFICATE REQUEST")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return SignedNode{}, fmt.Errorf("pki: CSR: %w", err)
	}
	if err := csr.CheckSignature(); err != nil {
		return SignedNode{}, fmt.Errorf("pki: CSR signature: %w", err)
	}
	uri, err := url.Parse(agentproto.NodeURIPrefix + nodeID.String())
	if err != nil {
		return SignedNode{}, err
	}
	now := ca.now()
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "node " + nodeID.String(), OrganizationalUnit: []string{hostname}},
		URIs:         []*url.URL{uri},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(NodeCertTTL),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, csr.PublicKey, ca.key)
	if err != nil {
		return SignedNode{}, fmt.Errorf("pki: sign node certificate: %w", err)
	}
	return SignedNode{
		CertPEM:     pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		Fingerprint: Fingerprint(der),
		NotAfter:    tmpl.NotAfter,
	}, nil
}

// ServerTLS returns a gateway TLS config: a server certificate signed by
// the CA for hosts (names and IPs), and mandatory client certificates
// verified against the CA.
func (ca *CA) ServerTLS(hosts []string) (*tls.Config, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, err
	}
	now := ca.now()
	tmpl := &x509.Certificate{
		SerialNumber: serial(),
		Subject:      pkix.Name{CommonName: "kondukter gateway"},
		NotBefore:    now.Add(-5 * time.Minute),
		NotAfter:     now.Add(365 * 24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, h := range hosts {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	if len(tmpl.IPAddresses)+len(tmpl.DNSNames) == 0 {
		return nil, errors.New("pki: gateway certificate needs at least one host name or IP")
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca.cert, &key.PublicKey, ca.key)
	if err != nil {
		return nil, fmt.Errorf("pki: sign gateway certificate: %w", err)
	}
	return &tls.Config{
		MinVersion:   tls.VersionTLS13,
		Certificates: []tls.Certificate{{Certificate: [][]byte{der, ca.cert.Raw}, PrivateKey: key}},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    ca.Pool(),
	}, nil
}

// NodeIDFromCert extracts the node id from a verified certificate's URI SAN.
func NodeIDFromCert(cert *x509.Certificate) (uuid.UUID, error) {
	for _, u := range cert.URIs {
		if rest, ok := strings.CutPrefix(u.String(), agentproto.NodeURIPrefix); ok {
			return uuid.Parse(rest)
		}
	}
	return uuid.Nil, errors.New("pki: certificate has no kondukter://node/ URI SAN")
}

// Fingerprint is the hex SHA-256 of a DER certificate.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	return hex.EncodeToString(sum[:])
}

func newCA() (certPEM, keyDER []byte, err error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	now := time.Now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial(),
		Subject:               pkix.Name{CommonName: "Kondukter internal CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err = x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), keyDER, nil
}

func sealer(masterKey string) (cipher.AEAD, error) {
	key, err := hkdf.Key(sha256.New, []byte(masterKey), []byte("kondukter-pki-v1"), "ca-key", 32)
	if err != nil {
		return nil, err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func seal(aead cipher.AEAD, plaintext []byte) []byte {
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		panic(err)
	}
	return aead.Seal(nonce, nonce, plaintext, nil)
}

func open(aead cipher.AEAD, ciphertext []byte) ([]byte, error) {
	n := aead.NonceSize()
	if len(ciphertext) < n {
		return nil, errors.New("pki: ciphertext too short")
	}
	return aead.Open(nil, ciphertext[:n], ciphertext[n:], nil)
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		panic(err)
	}
	return n
}
