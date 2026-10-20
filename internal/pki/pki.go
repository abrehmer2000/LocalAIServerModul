// Package pki provides an installation-local CA and distinct gateway/device identities.
package pki

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

const GatewayIdentity = "ki-basis-gateway"

type Authority struct {
	Cert *x509.Certificate
	Key  ed25519.PrivateKey
	PEM  []byte
}

func serial() *big.Int {
	n, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		panic(err)
	}
	return n
}
func PrivateKeyPEM(key ed25519.PrivateKey) ([]byte, error) {
	b, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: b}), nil
}
func New() (*Authority, error) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	now := time.Now()
	c := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: "KI-Basis installation CA"}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(10, 0, 0), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageCRLSign}
	der, err := x509.CreateCertificate(rand.Reader, c, c, pub, key)
	if err != nil {
		return nil, err
	}
	cert, err := x509.ParseCertificate(der)
	return &Authority{Cert: cert, Key: key, PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})}, err
}
func Load(certPath, keyPath string) (*Authority, error) {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return nil, errors.New("cannot load installation CA")
	}
	c, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil || !c.IsCA {
		return nil, errors.New("invalid installation CA")
	}
	key, ok := pair.PrivateKey.(ed25519.PrivateKey)
	if !ok {
		return nil, errors.New("installation CA requires Ed25519 key")
	}
	return &Authority{Cert: c, Key: key, PEM: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})}, nil
}
func (a *Authority) Sign(csrPEM []byte, id, host string, gateway bool) ([]byte, error) {
	block, _ := pem.Decode(csrPEM)
	if block == nil {
		return nil, errors.New("invalid CSR")
	}
	csr, err := x509.ParseCertificateRequest(block.Bytes)
	if err != nil {
		return nil, errors.New("invalid CSR")
	}
	if err = csr.CheckSignature(); err != nil {
		return nil, err
	}
	if _, ok := csr.PublicKey.(ed25519.PublicKey); !ok {
		return nil, errors.New("Ed25519 device key required")
	}
	now := time.Now()
	c := &x509.Certificate{SerialNumber: serial(), Subject: pkix.Name{CommonName: id}, NotBefore: now.Add(-5 * time.Minute), NotAfter: now.AddDate(0, 0, 90), KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth, x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
	if host != "" {
		if ip := net.ParseIP(host); ip != nil {
			c.IPAddresses = []net.IP{ip}
		} else {
			c.DNSNames = []string{host}
		}
	}
	if gateway {
		c.NotAfter = now.AddDate(1, 0, 0)
	}
	if c.NotAfter.After(a.Cert.NotAfter) {
		c.NotAfter = a.Cert.NotAfter
	}
	if !c.NotAfter.After(now.Add(time.Hour)) {
		return nil, errors.New("CA expires too soon")
	}
	der, err := x509.CreateCertificate(rand.Reader, c, a.Cert, csr.PublicKey, a.Key)
	if err != nil {
		return nil, err
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), nil
}
func CSR() (keyPEM, csrPEM []byte, err error) {
	pub, key, err := ed25519.GenerateKey(rand.Reader)
	_ = pub
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err = PrivateKeyPEM(key)
	if err != nil {
		return nil, nil, err
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{}, key)
	return keyPEM, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE REQUEST", Bytes: csr}), err
}

// Init writes into a newly created directory; an existing installation is never overwritten.
func Init(dir, host string) error {
	if host == "" || net.ParseIP(host) == nil && len(host) > 253 {
		return errors.New("valid host required")
	}
	if err := os.Mkdir(dir, 0700); err != nil {
		return err
	}
	a, err := New()
	if err != nil {
		return err
	}
	key, err := PrivateKeyPEM(a.Key)
	if err != nil {
		return err
	}
	gatewayKey, csr, err := CSR()
	if err != nil {
		return err
	}
	cert, err := a.Sign(csr, GatewayIdentity, host, true)
	if err != nil {
		return err
	}
	for name, data := range map[string][]byte{"ca.crt": a.PEM, "ca.key": key, "gateway.crt": cert, "gateway.key": gatewayKey} {
		if err = os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			return err
		}
	}
	return nil
}
func Roots(data []byte) (*x509.CertPool, error) {
	p := x509.NewCertPool()
	if !p.AppendCertsFromPEM(data) {
		return nil, errors.New("invalid CA certificates")
	}
	return p, nil
}
