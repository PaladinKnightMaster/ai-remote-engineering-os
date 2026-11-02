package main

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"aireos.local/services/api/internal/setup"
)

func writeTestCA(t *testing.T) (path string, der []byte) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "test local CA"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign,
	}
	der, err = x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), "root.crt")
	if err := os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	return path, der
}

// ADR-0078 §D7: the fingerprint is the SHA-256 of the DER, whether the file is PEM or DER.
func TestCAFingerprint(t *testing.T) {
	pemPath, der := writeTestCA(t)
	sum := sha256.Sum256(der)
	want := hex.EncodeToString(sum[:])
	if got, err := caFingerprint(pemPath); err != nil || got != want {
		t.Fatalf("PEM: got %q, %v; want %q", got, err, want)
	}
	derPath := filepath.Join(t.TempDir(), "root.der")
	if err := os.WriteFile(derPath, der, 0o600); err != nil {
		t.Fatal(err)
	}
	if got, err := caFingerprint(derPath); err != nil || got != want {
		t.Fatalf("DER: got %q, %v; want %q", got, err, want)
	}
	junk := filepath.Join(t.TempDir(), "junk")
	if err := os.WriteFile(junk, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := caFingerprint(junk); err == nil {
		t.Fatal("a non-certificate produced a fingerprint")
	}
}

// With the operator in the environment, setup mode is never entered (ADR-0078 §D4), so
// issue refuses BEFORE touching the database (the empty DSN would fail to connect) and
// prints nothing.
func TestSetupCodeIssueRefusedWhenEnvOperatorSet(t *testing.T) {
	t.Setenv("AIREOS_BOOTSTRAP_OPERATOR_USER", "admin")
	t.Setenv("AIREOS_BOOTSTRAP_OPERATOR_PASSWORD_HASH", "$argon2id$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA")
	var out bytes.Buffer
	err := runSetupCode("", []string{"issue"}, &out)
	if !errors.Is(err, setup.ErrOperatorExists) || out.Len() != 0 {
		t.Fatalf("err = %v, stdout = %q; want ErrOperatorExists and no output", err, out.String())
	}
	if err := runSetupCode("", []string{"nonsense"}, &out); err == nil {
		t.Fatal("unknown setup-code action accepted")
	}
}
