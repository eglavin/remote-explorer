// Package tlscert creates the self-signed certificate served when no
// certificate is supplied.
package tlscert

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"time"
)

// Validity is long enough to outlast any realistic run, since the
// certificate is replaced on every start anyway.
const Validity = 365 * 24 * time.Hour

// SelfSigned creates a key pair and a certificate for names and ips.
func SelfSigned(names []string, ips []net.IP, now time.Time) (tls.Certificate, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, err
	}
	tmpl := &x509.Certificate{
		// A nil SerialNumber makes CreateCertificate pick a random one.
		Subject: pkix.Name{CommonName: "remote-explorer"},
		// Backdated so a client whose clock runs a little behind still accepts it.
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(Validity),
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              names,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, err
	}
	leaf, err := x509.ParseCertificate(der)
	if err != nil {
		return tls.Certificate{}, err
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key, Leaf: leaf}, nil
}

// Names returns the host names and addresses clients may use to reach a
// server listening on ip, plus extra. An unspecified ip (0.0.0.0 or ::)
// covers every address of every interface.
func Names(ip net.IP, extra []string) ([]string, []net.IP) {
	names := []string{"localhost"}
	if h, err := os.Hostname(); err == nil && h != "" {
		names = append(names, strings.ToLower(h))
	}
	names = append(names, extra...)
	slices.Sort(names)
	names = slices.Compact(names)

	ips := []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback}
	add := func(ip net.IP) {
		if !slices.ContainsFunc(ips, ip.Equal) {
			ips = append(ips, ip)
		}
	}
	if ip.IsUnspecified() {
		addrs, _ := net.InterfaceAddrs()
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok {
				add(n.IP)
			}
		}
	} else {
		add(ip)
	}
	return names, ips
}

// Fingerprint is the SHA-256 hash of the certificate in the colon-separated
// form browsers show in their certificate viewers.
func Fingerprint(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = fmt.Sprintf("%02X", b)
	}
	return strings.Join(parts, ":")
}

// PublicKeyPin is the value for curl's --pinnedpubkey, which curl checks
// even with -k, so it can trust this certificate without trusting any other.
func PublicKeyPin(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.RawSubjectPublicKeyInfo)
	return "sha256//" + base64.StdEncoding.EncodeToString(sum[:])
}
