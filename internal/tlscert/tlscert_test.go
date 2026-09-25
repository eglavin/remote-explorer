package tlscert

import (
	"crypto/tls"
	"crypto/x509"
	"net"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestSelfSignedVerifies(t *testing.T) {
	now := time.Now()
	cert, err := SelfSigned([]string{"localhost", "nas.lan"}, []net.IP{net.IPv4(127, 0, 0, 1), net.ParseIP("192.168.1.5")}, now)
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AddCert(cert.Leaf)
	for _, name := range []string{"localhost", "nas.lan", "127.0.0.1", "192.168.1.5"} {
		if _, err := cert.Leaf.Verify(x509.VerifyOptions{DNSName: name, Roots: pool, CurrentTime: now}); err != nil {
			t.Errorf("verify for %s: %v", name, err)
		}
	}
	if _, err := cert.Leaf.Verify(x509.VerifyOptions{DNSName: "other.lan", Roots: pool, CurrentTime: now}); err == nil {
		t.Error("certificate verified for a name it does not cover")
	}
}

func TestSelfSignedServesTLS(t *testing.T) {
	cert, err := SelfSigned([]string{"localhost"}, nil, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err == nil {
			_ = c.(*tls.Conn).Handshake()
			c.Close()
		}
	}()

	pool := x509.NewCertPool()
	pool.AddCert(cert.Leaf)
	conn, err := tls.Dial("tcp", ln.Addr().String(), &tls.Config{RootCAs: pool, ServerName: "localhost"})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
}

func TestSelfSignedIsNewEachTime(t *testing.T) {
	a, _ := SelfSigned([]string{"localhost"}, nil, time.Now())
	b, _ := SelfSigned([]string{"localhost"}, nil, time.Now())
	if PublicKeyPin(a.Leaf) == PublicKeyPin(b.Leaf) || a.Leaf.SerialNumber.Cmp(b.Leaf.SerialNumber) == 0 {
		t.Error("two certificates share a key or serial number")
	}
}

func TestNames(t *testing.T) {
	names, ips := Names(net.IPv4(127, 0, 0, 1), []string{"nas.lan", "localhost"})
	if !slices.Contains(names, "localhost") || !slices.Contains(names, "nas.lan") {
		t.Errorf("names = %v", names)
	}
	if n := len(names); n != len(slices.Compact(slices.Clone(names))) {
		t.Errorf("duplicate names: %v", names)
	}
	if len(ips) != 2 {
		t.Errorf("loopback listener: ips = %v, want only the loopback addresses", ips)
	}

	_, ips = Names(net.ParseIP("192.168.1.5"), nil)
	if !slices.ContainsFunc(ips, net.ParseIP("192.168.1.5").Equal) {
		t.Errorf("ips = %v, want the listen address", ips)
	}

	_, ips = Names(net.IPv4zero, nil)
	for i, ip := range ips {
		if slices.ContainsFunc(ips[i+1:], ip.Equal) {
			t.Errorf("duplicate address %s in %v", ip, ips)
		}
	}
}

func TestFingerprintAndPin(t *testing.T) {
	cert, _ := SelfSigned([]string{"localhost"}, nil, time.Now())
	fp := Fingerprint(cert.Leaf)
	if len(fp) != 32*3-1 || strings.Count(fp, ":") != 31 || fp != strings.ToUpper(fp) {
		t.Errorf("fingerprint = %q", fp)
	}
	if pin := PublicKeyPin(cert.Leaf); !strings.HasPrefix(pin, "sha256//") || len(pin) != len("sha256//")+44 {
		t.Errorf("pin = %q", pin)
	}
}
