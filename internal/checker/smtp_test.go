package checker

import (
	"bufio"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"uptime-monitor/internal/model"
)

// Helper to start a mock SMTP server
func startMockSMTPServer(t *testing.T, greetingLines []string) (string, func()) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}

	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-done:
					return
				default:
					return
				}
			}

			go func(c net.Conn) {
				defer c.Close()
				for _, line := range greetingLines {
					_, _ = fmt.Fprintf(c, "%s\r\n", line)
				}
				reader := bufio.NewReader(c)
				cmd, _ := reader.ReadString('\n')
				if strings.HasPrefix(strings.ToUpper(cmd), "QUIT") {
					_, _ = fmt.Fprintf(c, "221 2.0.0 Bye\r\n")
				}
			}(conn)
		}
	}()

	cleanup := func() {
		close(done)
		_ = ln.Close()
	}

	return ln.Addr().String(), cleanup
}

func generateTestCert(t *testing.T) tls.Certificate {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses:           []net.IP{net.ParseIP("127.0.0.1")},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	return tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  priv,
	}
}

func startMockTLSSMTPServer(t *testing.T, greetingLines []string) (string, func()) {
	cert := generateTestCert(t)
	tlsConfig := &tls.Config{
		Certificates: []tls.Certificate{cert},
	}

	ln, err := tls.Listen("tcp", "127.0.0.1:0", tlsConfig)
	if err != nil {
		t.Fatalf("failed to listen TLS: %v", err)
	}

	done := make(chan struct{})
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				select {
				case <-done:
					return
				default:
					return
				}
			}

			go func(c net.Conn) {
				defer c.Close()
				for _, line := range greetingLines {
					_, _ = fmt.Fprintf(c, "%s\r\n", line)
				}
				reader := bufio.NewReader(c)
				cmd, _ := reader.ReadString('\n')
				if strings.HasPrefix(strings.ToUpper(cmd), "QUIT") {
					_, _ = fmt.Fprintf(c, "221 2.0.0 Bye\r\n")
				}
			}(conn)
		}
	}()

	cleanup := func() {
		close(done)
		_ = ln.Close()
	}

	return ln.Addr().String(), cleanup
}

func TestSMTPProber_StandardGreeting(t *testing.T) {
	addr, cleanup := startMockSMTPServer(t, []string{"220 mail.example.com ESMTP Postfix"})
	defer cleanup()

	prober := NewSMTPProber()
	m := &model.Monitor{
		Target:     "smtp://" + addr,
		TimeoutSec: 2,
	}

	res := prober.Probe(context.Background(), m)
	if !res.IsSuccess {
		t.Fatalf("expected success, got error: %s", res.ErrorMessage)
	}
	if res.StatusCode != 220 {
		t.Fatalf("expected status code 220, got %d", res.StatusCode)
	}
	if !strings.Contains(res.FailureDetails.ResponseBodyHead, "mail.example.com") {
		t.Fatalf("expected banner in details, got: %s", res.FailureDetails.ResponseBodyHead)
	}
}

func TestSMTPProber_MultilineGreeting(t *testing.T) {
	addr, cleanup := startMockSMTPServer(t, []string{
		"220-mail.example.com ESMTP ready",
		"220-STARTTLS PIPELINING",
		"220 Ready to accept connections",
	})
	defer cleanup()

	prober := NewSMTPProber()
	m := &model.Monitor{
		Target:     addr,
		TimeoutSec: 2,
	}

	res := prober.Probe(context.Background(), m)
	if !res.IsSuccess {
		t.Fatalf("expected success, got error: %s", res.ErrorMessage)
	}
	if !strings.Contains(res.FailureDetails.ResponseBodyHead, "Ready to accept connections") {
		t.Fatalf("expected multiline banner in details, got: %s", res.FailureDetails.ResponseBodyHead)
	}
}

func TestSMTPProber_KeywordAssertion(t *testing.T) {
	addr, cleanup := startMockSMTPServer(t, []string{"220 tmail.sagasoft.xyz Stalwart ESMTP at your service"})
	defer cleanup()

	prober := NewSMTPProber()

	// Matching keyword
	mSuccess := &model.Monitor{
		Target:     addr,
		Keyword:    "Stalwart",
		TimeoutSec: 2,
	}
	resSuccess := prober.Probe(context.Background(), mSuccess)
	if !resSuccess.IsSuccess {
		t.Fatalf("expected keyword success, got: %s", resSuccess.ErrorMessage)
	}

	// Missing keyword
	mFail := &model.Monitor{
		Target:     addr,
		Keyword:    "SendmailNonExistent",
		TimeoutSec: 2,
	}
	resFail := prober.Probe(context.Background(), mFail)
	if resFail.IsSuccess {
		t.Fatalf("expected failure for missing keyword")
	}
	if resFail.ErrorType != "KEYWORD_MISSING" {
		t.Fatalf("expected KEYWORD_MISSING, got: %s", resFail.ErrorType)
	}
}

func TestSMTPProber_InvalidBanner(t *testing.T) {
	addr, cleanup := startMockSMTPServer(t, []string{"554 5.7.1 Service unavailable; Client host blocked"})
	defer cleanup()

	prober := NewSMTPProber()
	m := &model.Monitor{
		Target:     addr,
		TimeoutSec: 2,
	}

	res := prober.Probe(context.Background(), m)
	if res.IsSuccess {
		t.Fatalf("expected failure for 554 banner")
	}
	if res.ErrorType != "SMTP_BANNER_INVALID" {
		t.Fatalf("expected SMTP_BANNER_INVALID, got: %s", res.ErrorType)
	}
}

func TestSMTPProber_TLSImplicit(t *testing.T) {
	addr, cleanup := startMockTLSSMTPServer(t, []string{"220 smtps.example.com Secure ESMTP"})
	defer cleanup()

	prober := NewSMTPProber()
	m := &model.Monitor{
		Target:        "smtps://" + addr,
		SkipTLSVerify: true,
		TimeoutSec:    2,
	}

	res := prober.Probe(context.Background(), m)
	if !res.IsSuccess {
		t.Fatalf("expected TLS probe success, got: %s", res.ErrorMessage)
	}
	if res.FailureDetails.ProbePhase != "SMTP_SUCCESS" {
		t.Fatalf("expected phase SMTP_SUCCESS, got %s", res.FailureDetails.ProbePhase)
	}
	if res.FailureDetails.CertIssuer == "" {
		t.Fatalf("expected CertIssuer populated, got empty")
	}
}

func TestSMTPProber_LocalStalwartIntegration(t *testing.T) {
	// Probe local Stalwart running on 127.0.0.1:25
	prober := NewSMTPProber()
	m := &model.Monitor{
		Target:     "127.0.0.1:25",
		Keyword:    "Stalwart",
		TimeoutSec: 3,
	}

	res := prober.Probe(context.Background(), m)
	if !res.IsSuccess {
		t.Logf("Local stalwart test skipped/informational: %s", res.ErrorMessage)
		return
	}
	if res.StatusCode != 220 {
		t.Fatalf("expected 220 status code, got %d", res.StatusCode)
	}
	if !strings.Contains(res.FailureDetails.ResponseBodyHead, "Stalwart") {
		t.Fatalf("expected banner to contain Stalwart, got: %s", res.FailureDetails.ResponseBodyHead)
	}
}
