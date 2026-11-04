package checker

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"uptime-monitor/internal/model"
)

type TCPProber struct{}

func NewTCPProber() *TCPProber {
	return &TCPProber{}
}

// normalizeTCPTarget strips schemes and ensures valid host:port format, handling IPv6 brackets
func normalizeTCPTarget(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("empty TCP target")
	}
	// Strip scheme if provided, e.g. tcp:// or TCP://
	if idx := strings.Index(raw, "://"); idx != -1 {
		raw = raw[idx+3:]
	}
	raw = strings.TrimRight(raw, "/")

	// If it already parses cleanly as host:port
	if host, port, err := net.SplitHostPort(raw); err == nil {
		if host == "" {
			host = "127.0.0.1"
		}
		if port == "" {
			port = "80"
		}
		return net.JoinHostPort(host, port), nil
	}

	// Handle bare IPv6 like "::1" or "[::1]" or IPv4 "1.2.3.4"
	trimmed := strings.Trim(raw, "[]")
	if ip := net.ParseIP(trimmed); ip != nil {
		return net.JoinHostPort(trimmed, "80"), nil
	}

	// Bare hostname or prefix
	if strings.HasSuffix(raw, ":") {
		return raw + "80", nil
	}
	return net.JoinHostPort(raw, "80"), nil
}

func (p *TCPProber) Probe(ctx context.Context, m *model.Monitor) *model.CheckResult {
	timeout := time.Duration(m.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	diag := &model.DiagnosticDetail{
		ProbePhase: "TCP_CONNECT",
	}

	target, err := normalizeTCPTarget(m.Target)
	if err != nil {
		diag.ErrorMessage = err.Error()
		return &model.CheckResult{
			IsSuccess:      false,
			ErrorType:      "INVALID_TARGET",
			ErrorMessage:   err.Error(),
			FailureDetails: diag,
		}
	}

	start := time.Now()
	var d net.Dialer
	d.Timeout = timeout

	conn, err := d.DialContext(ctx, "tcp", target)
	latency := time.Since(start).Milliseconds()
	diag.TCPConnectMs = latency

	if err != nil {
		diag.ErrorMessage = err.Error()
		errStr := strings.ToLower(err.Error())
		errType := "TCP_ERROR"
		if strings.Contains(errStr, "timeout") || strings.Contains(errStr, "deadline exceeded") {
			errType = "TIMEOUT"
		} else if strings.Contains(errStr, "connection refused") {
			errType = "CONN_REFUSED"
		} else if strings.Contains(errStr, "connection reset") {
			errType = "CONN_RESET"
		} else if strings.Contains(errStr, "no such host") {
			errType = "DNS_ERROR"
		}

		return &model.CheckResult{
			IsSuccess:      false,
			LatencyMs:      latency,
			ErrorType:      errType,
			ErrorMessage:   fmt.Sprintf("TCP dial failed: %v", err),
			FailureDetails: diag,
		}
	}
	defer conn.Close()

	return &model.CheckResult{
		IsSuccess:      true,
		StatusCode:     0,
		LatencyMs:      latency,
		FailureDetails: diag,
	}
}
