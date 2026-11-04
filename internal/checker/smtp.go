package checker

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"time"

	"uptime-monitor/internal/model"
)

type SMTPProber struct{}

func NewSMTPProber() *SMTPProber {
	return &SMTPProber{}
}

func (p *SMTPProber) Probe(ctx context.Context, m *model.Monitor) *model.CheckResult {
	timeout := time.Duration(m.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	diag := &model.DiagnosticDetail{
		ProbePhase: "SMTP_CONNECT",
	}

	rawTarget := strings.TrimSpace(m.Target)
	isTLS := false

	if strings.HasPrefix(strings.ToLower(rawTarget), "smtps://") {
		isTLS = true
		rawTarget = strings.TrimPrefix(rawTarget, "smtps://")
		rawTarget = strings.TrimPrefix(rawTarget, "SMTPS://")
	} else if strings.HasPrefix(strings.ToLower(rawTarget), "smtp://") {
		rawTarget = strings.TrimPrefix(rawTarget, "smtp://")
		rawTarget = strings.TrimPrefix(rawTarget, "SMTP://")
	}

	rawTarget = strings.TrimRight(rawTarget, "/")

	host, port, err := net.SplitHostPort(rawTarget)
	if err != nil {
		host = rawTarget
		if isTLS {
			port = "465"
		} else {
			port = "25"
		}
	}

	if port == "465" {
		isTLS = true
	}

	target := net.JoinHostPort(host, port)
	start := time.Now()

	// DNS lookup timing
	dnsStart := time.Now()
	_, dnsErr := net.LookupHost(host)
	diag.DNSLookupMs = time.Since(dnsStart).Milliseconds()
	if dnsErr != nil {
		diag.ErrorMessage = fmt.Sprintf("DNS lookup failed for %s: %v", host, dnsErr)
		return &model.CheckResult{
			IsSuccess:      false,
			LatencyMs:      time.Since(start).Milliseconds(),
			ErrorType:      "DNS_RESOLUTION_FAILED",
			ErrorMessage:   diag.ErrorMessage,
			FailureDetails: diag,
		}
	}

	var d net.Dialer
	d.Timeout = timeout

	var conn net.Conn
	tcpStart := time.Now()

	if isTLS {
		diag.ProbePhase = "SMTP_TLS_CONNECT"
		tlsDialer := &tls.Dialer{
			NetDialer: &d,
			Config: &tls.Config{
				InsecureSkipVerify: m.SkipTLSVerify,
				ServerName:         host,
			},
		}
		tlsConn, err := tlsDialer.DialContext(ctx, "tcp", target)
		diag.TCPConnectMs = time.Since(tcpStart).Milliseconds()
		if err != nil {
			diag.ErrorMessage = err.Error()
			return &model.CheckResult{
				IsSuccess:      false,
				LatencyMs:      time.Since(start).Milliseconds(),
				ErrorType:      "SMTP_CONNECT_FAILED",
				ErrorMessage:   fmt.Sprintf("SMTP TLS connection failed: %v", err),
				FailureDetails: diag,
			}
		}
		conn = tlsConn
		if tc, ok := conn.(*tls.Conn); ok {
			state := tc.ConnectionState()
			if len(state.PeerCertificates) > 0 {
				cert := state.PeerCertificates[0]
				diag.CertIssuer = cert.Issuer.CommonName
				diag.CertExpiry = cert.NotAfter.Format(time.RFC3339)
				diag.CertExpiryDays = int(time.Until(cert.NotAfter).Hours() / 24)
			}
		}
	} else {
		diag.ProbePhase = "SMTP_CONNECT"
		c, err := d.DialContext(ctx, "tcp", target)
		diag.TCPConnectMs = time.Since(tcpStart).Milliseconds()
		if err != nil {
			diag.ErrorMessage = err.Error()
			return &model.CheckResult{
				IsSuccess:      false,
				LatencyMs:      time.Since(start).Milliseconds(),
				ErrorType:      "SMTP_CONNECT_FAILED",
				ErrorMessage:   fmt.Sprintf("SMTP connection failed: %v", err),
				FailureDetails: diag,
			}
		}
		conn = c
	}
	defer conn.Close()

	_ = conn.SetDeadline(time.Now().Add(timeout))

	diag.ProbePhase = "SMTP_GREETING"
	reader := bufio.NewReader(conn)

	// Read greeting banner (supporting RFC 5321 multiline continuation lines e.g. "220-...")
	var bannerBuilder strings.Builder
	var lastLine string
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			diag.ErrorMessage = err.Error()
			return &model.CheckResult{
				IsSuccess:      false,
				LatencyMs:      time.Since(start).Milliseconds(),
				ErrorType:      "SMTP_GREETING_TIMEOUT",
				ErrorMessage:   fmt.Sprintf("failed to read SMTP greeting: %v", err),
				FailureDetails: diag,
			}
		}
		bannerBuilder.WriteString(line)
		lastLine = line
		trimmed := strings.TrimSpace(line)
		// If line does not have continuation hyphen at index 3 (e.g. 220-), banner is complete
		if len(trimmed) < 4 || trimmed[3] != '-' {
			break
		}
	}

	latency := time.Since(start).Milliseconds()
	greeting := strings.TrimSpace(bannerBuilder.String())
	diag.ResponseBodyHead = greeting

	// The last line of the initial greeting banner should start with 220
	if !strings.HasPrefix(strings.TrimSpace(lastLine), "220") {
		diag.ErrorMessage = fmt.Sprintf("invalid SMTP banner, expected 220: %s", greeting)
		return &model.CheckResult{
			IsSuccess:      false,
			LatencyMs:      latency,
			ErrorType:      "SMTP_BANNER_INVALID",
			ErrorMessage:   diag.ErrorMessage,
			FailureDetails: diag,
		}
	}

	// Keyword assertion if configured
	if m.Keyword != "" && !strings.Contains(greeting, m.Keyword) {
		diag.ErrorMessage = fmt.Sprintf("keyword %q not found in SMTP greeting banner: %s", m.Keyword, greeting)
		return &model.CheckResult{
			IsSuccess:      false,
			LatencyMs:      latency,
			StatusCode:     220,
			ErrorType:      "KEYWORD_MISSING",
			ErrorMessage:   diag.ErrorMessage,
			FailureDetails: diag,
		}
	}

	// Politely send QUIT
	diag.ProbePhase = "SMTP_QUIT"
	_, _ = fmt.Fprintf(conn, "QUIT\r\n")

	diag.ProbePhase = "SMTP_SUCCESS"
	return &model.CheckResult{
		IsSuccess:      true,
		StatusCode:     220,
		LatencyMs:      latency,
		FailureDetails: diag,
	}
}
