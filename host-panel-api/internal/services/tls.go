package services

import (
	"crypto/ecdsa"
	"crypto/elliptic"
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
	"slices"
	"strings"
	"time"
)

// TLSMode is SERVICES_TLS.
type TLSMode string

const (
	// TLSAuto requires TLS on every instance published on a non-loopback
	// address (for all its clients, the sites included).
	TLSAuto TLSMode = "auto"
	// TLSOff disables TLS everywhere, deliberately (clear-text passwords on
	// the published ports).
	TLSOff TLSMode = "off"
)

// TLS files of an instance, in CertsDir, mounted read-only at TLSMount.
const (
	CertsDir = "certs"
	CertFile = "server.crt"
	KeyFile  = "server.key"
	TLSMount = "/etc/loch/tls"

	certValidity    = 365 * 24 * time.Hour
	certRenewBefore = 30 * 24 * time.Hour
)

// TLSCapable engines can require TLS. The Manager writes the certificate
// (CertsDir, mounted at TLSMount); the engine returns its settings.
type TLSCapable interface {
	// TLS returns the settings of a version, false when it cannot do TLS.
	TLS(ref InstanceRef, v Version) (TLSSpec, bool)
}

// OptionalTLSCapable engines can also offer TLS without requiring it: clients
// choose (SERVICES_ALLOW_TLS_OPTIONAL, chosen per exposure). Passwords and data of plain clients then
// travel in clear on the published port.
type OptionalTLSCapable interface {
	// TLSOptional returns the settings offering TLS, false when the version
	// cannot.
	TLSOptional(ref InstanceRef, v Version) (TLSSpec, bool)
}

// supportsOptionalTLS reports whether an engine version can offer TLS
// without requiring it.
func supportsOptionalTLS(e Engine, ref InstanceRef, v Version) bool {
	tc, ok := e.(OptionalTLSCapable)
	if !ok || !supportsTLS(e, ref, v) {
		return false
	}
	_, ok = tc.TLSOptional(ref, v)
	return ok
}

// supportsTLS reports whether an engine version can require TLS.
func supportsTLS(e Engine, ref InstanceRef, v Version) bool {
	tc, ok := e.(TLSCapable)
	if !ok {
		return false
	}
	_, ok = tc.TLS(ref, v)
	return ok
}

// tlsSpec returns the TLS settings of a version (empty when unsupported):
// TLS offered only when optional, required otherwise.
func tlsSpec(e Engine, ref InstanceRef, v Version, optional bool) TLSSpec {
	if optional {
		if tc, ok := e.(OptionalTLSCapable); ok {
			if spec, ok := tc.TLSOptional(ref, v); ok {
				return spec
			}
		}
		return TLSSpec{}
	}
	if tc, ok := e.(TLSCapable); ok {
		if spec, ok := tc.TLS(ref, v); ok {
			return spec
		}
	}
	return TLSSpec{}
}

// TLSSpec is what an engine changes to require TLS.
type TLSSpec struct {
	Args []string // appended to the command
	// Files replace the generated files mounted at the same targets (e.g.
	// a pg_hba.conf requiring hostssl); their names must differ.
	Files []GeneratedFile
}

// Certificate is a PEM certificate and its key.
type Certificate struct {
	CertPEM []byte
	KeyPEM  []byte
}

// selfSignedCertificate creates an ECDSA P-256 certificate for the names.
func selfSignedCertificate(names []string, now time.Time) (Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return Certificate{}, err
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 127))
	if err != nil {
		return Certificate{}, err
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: names[0], Organization: []string{"Loch.sh Panel"}},
		NotBefore:    now.Add(-time.Hour),
		NotAfter:     now.Add(certValidity),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	for _, n := range names {
		if ip := net.ParseIP(n); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, n)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return Certificate{}, err
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return Certificate{}, err
	}
	return Certificate{
		CertPEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		KeyPEM:  pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}),
	}, nil
}

// parseCertificate decodes the first certificate of a PEM file.
func parseCertificate(certPEM []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(certPEM)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("no PEM certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

// certificateUsable reports whether a self-signed certificate can be kept:
// valid for long enough and covering the names.
func certificateUsable(certPEM []byte, names []string, now time.Time) bool {
	cert, err := parseCertificate(certPEM)
	if err != nil || now.Add(certRenewBefore).After(cert.NotAfter) {
		return false
	}
	for _, n := range names {
		if cert.VerifyHostname(n) != nil {
			return false
		}
	}
	return true
}

// Fingerprint is the SHA-256 fingerprint of a PEM certificate, shown to
// clients so they can check a self-signed certificate.
func Fingerprint(certPEM []byte) string {
	cert, err := parseCertificate(certPEM)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(cert.Raw)
	return strings.ToUpper(hex.EncodeToString(sum[:]))
}

// certificateNames are the names a certificate must cover.
func certificateNames(publicHost string, ref InstanceRef) []string {
	names := []string{}
	if publicHost != "" {
		names = append(names, publicHost)
	}
	if !slices.Contains(names, ref.Project()) {
		names = append(names, ref.Project())
	}
	return names
}

// ValidateCertificate checks a certificate and key provided by the
// administrator (SERVICES_TLS_CERT_FILE / SERVICES_TLS_KEY_FILE): they must
// form a pair (an encrypted key is refused: the engines cannot read it).
func ValidateCertificate(c Certificate, now time.Time) error {
	if _, err := tls.X509KeyPair(c.CertPEM, c.KeyPEM); err != nil {
		return fmt.Errorf("certificate and key: %w", err)
	}
	cert, err := parseCertificate(c.CertPEM)
	if err != nil {
		return fmt.Errorf("certificate: %w", err)
	}
	if now.After(cert.NotAfter) {
		return errors.New("certificate expired")
	}
	return nil
}
