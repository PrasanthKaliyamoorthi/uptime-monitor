package checker

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"strings"
	"time"

	"uptime-monitor/internal/model"
)

type HTTPProber struct{}

func NewHTTPProber() *HTTPProber {
	return &HTTPProber{}
}

// normalizeHTTPTarget ensures the URL contains an explicit scheme
func normalizeHTTPTarget(raw string, defaultProtocol string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		scheme := "https"
		if strings.ToLower(defaultProtocol) == "http" {
			scheme = "http"
		}
		return scheme + "://" + raw
	}
	return raw
}

func (p *HTTPProber) Probe(ctx context.Context, m *model.Monitor) *model.CheckResult {
	timeout := time.Duration(m.TimeoutSec) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var dnsStart, tcpStart, tlsStart time.Time

	diag := &model.DiagnosticDetail{
		ProbePhase: "DNS",
	}

	trace := &httptrace.ClientTrace{
		DNSStart: func(_ httptrace.DNSStartInfo) {
			dnsStart = time.Now()
		},
		DNSDone: func(_ httptrace.DNSDoneInfo) {
			diag.DNSLookupMs = time.Since(dnsStart).Milliseconds()
		},
		ConnectStart: func(_, _ string) {
			tcpStart = time.Now()
			diag.ProbePhase = "TCP_CONNECT"
		},
		ConnectDone: func(_, _ string, _ error) {
			diag.TCPConnectMs = time.Since(tcpStart).Milliseconds()
		},
		TLSHandshakeStart: func() {
			tlsStart = time.Now()
			diag.ProbePhase = "TLS_HANDSHAKE"
		},
		TLSHandshakeDone: func(state tls.ConnectionState, err error) {
			diag.TLSHandshakeMs = time.Since(tlsStart).Milliseconds()
			if err == nil && len(state.PeerCertificates) > 0 {
				cert := state.PeerCertificates[0]
				diag.CertIssuer = cert.Issuer.CommonName
				diag.CertExpiry = cert.NotAfter.UTC().Format(time.RFC3339)
				daysRemaining := int(time.Until(cert.NotAfter).Hours() / 24)
				diag.CertExpiryDays = daysRemaining
			}
		},
	}

	traceCtx := httptrace.WithClientTrace(ctx, trace)

	method := strings.ToUpper(strings.TrimSpace(m.Method))
	if method == "" {
		method = "GET"
	}

	var bodyReader io.Reader
	if len(m.Body) > 0 && (method == "POST" || method == "PUT" || method == "PATCH") {
		bodyReader = strings.NewReader(m.Body)
	}

	target := normalizeHTTPTarget(m.Target, m.Protocol)
	req, err := http.NewRequestWithContext(traceCtx, method, target, bodyReader)
	if err != nil {
		diag.ErrorMessage = fmt.Sprintf("invalid request URL: %v", err)
		return &model.CheckResult{
			IsSuccess:      false,
			ErrorType:      "INVALID_URL",
			ErrorMessage:   diag.ErrorMessage,
			FailureDetails: diag,
		}
	}

	// Apply Authentication
	switch m.AuthType {
	case "bearer":
		if m.AuthToken != "" {
			req.Header.Set("Authorization", "Bearer "+m.AuthToken)
		}
	case "basic":
		if m.AuthUser != "" || m.AuthPass != "" {
			req.SetBasicAuth(m.AuthUser, m.AuthPass)
		}
	}

	// Apply Custom Headers
	if m.HeadersJSON != "" {
		var headers map[string]string
		if err := json.Unmarshal([]byte(m.HeadersJSON), &headers); err == nil {
			for k, v := range headers {
				req.Header.Set(k, v)
			}
		}
	}

	// Default User-Agent if absent
	if req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", "UptimeMonitor/1.0 (+https://github.com/openstatus)")
	}

	// Transport configuration
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   timeout,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: m.SkipTLSVerify,
		},
		DisableKeepAlives: true,
	}

	client := &http.Client{
		Transport: transport,
		Timeout:   timeout,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("stopped after 10 redirects")
			}
			return nil
		},
	}

	startTime := time.Now()
	diag.ProbePhase = "HTTP_REQUEST"
	resp, err := client.Do(req)
	latency := time.Since(startTime).Milliseconds()

	if err != nil {
		diag.ErrorMessage = err.Error()
		errStr := strings.ToLower(err.Error())
		errType := "HTTP_ERROR"
		if strings.Contains(errStr, "context deadline exceeded") || strings.Contains(errStr, "client.timeout") || strings.Contains(errStr, "timeout") {
			errType = "TIMEOUT"
		} else if strings.Contains(errStr, "no such host") || strings.Contains(errStr, "server misbehaving") {
			errType = "DNS_ERROR"
		} else if strings.Contains(errStr, "connection refused") {
			errType = "CONN_REFUSED"
		} else if strings.Contains(errStr, "connection reset") || strings.Contains(errStr, "broken pipe") {
			errType = "CONN_RESET"
		} else if strings.Contains(errStr, "stopped after 10 redirects") {
			errType = "REDIRECT_LOOP"
		} else if strings.Contains(errStr, "certificate has expired") || strings.Contains(errStr, "x509: certificate has expired") {
			errType = "CERT_EXPIRED"
		} else if strings.Contains(errStr, "certificate is valid for") || strings.Contains(errStr, "cannot validate certificate for") {
			errType = "CERT_HOST_MISMATCH"
		} else if strings.Contains(errStr, "certificate signed by unknown authority") {
			errType = "CERT_UNTRUSTED"
		} else if strings.Contains(errStr, "certificate") || strings.Contains(errStr, "tls:") || strings.Contains(errStr, "handshake failure") {
			errType = "TLS_ERROR"
		}

		return &model.CheckResult{
			IsSuccess:      false,
			LatencyMs:      latency,
			ErrorType:      errType,
			ErrorMessage:   err.Error(),
			FailureDetails: diag,
		}
	}
	defer resp.Body.Close()

	diag.StatusCode = resp.StatusCode
	diag.ExpectedCode = m.ExpectedCode

	// Capture response headers snippet
	headersMap := make(map[string]string)
	for k, vals := range resp.Header {
		if len(vals) > 0 {
			headersMap[k] = vals[0]
		}
	}
	diag.ResponseHeaders = headersMap

	// Read body (up to 1MB total, with first 1KB saved as snippet)
	bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	bodyStr := string(bodyBytes)
	if len(bodyStr) > 1024 {
		diag.ResponseBodyHead = bodyStr[:1024] + " ... [truncated]"
	} else {
		diag.ResponseBodyHead = bodyStr
	}

	// Check HTTP status code
	expectedCode := m.ExpectedCode
	if expectedCode <= 0 {
		expectedCode = 200
	}
	if resp.StatusCode != expectedCode {
		diag.ErrorMessage = fmt.Sprintf("expected status %d, got %d", expectedCode, resp.StatusCode)
		return &model.CheckResult{
			IsSuccess:      false,
			StatusCode:     resp.StatusCode,
			LatencyMs:      latency,
			ErrorType:      "HTTP_CODE_MISMATCH",
			ErrorMessage:   diag.ErrorMessage,
			FailureDetails: diag,
		}
	}

	// Check keyword assertion if configured
	if m.Keyword != "" {
		if !strings.Contains(bodyStr, m.Keyword) {
			diag.ErrorMessage = fmt.Sprintf("keyword %q not found in response body", m.Keyword)
			return &model.CheckResult{
				IsSuccess:      false,
				StatusCode:     resp.StatusCode,
				LatencyMs:      latency,
				ErrorType:      "KEYWORD_MISSING",
				ErrorMessage:   diag.ErrorMessage,
				FailureDetails: diag,
			}
		}
	}

	return &model.CheckResult{
		IsSuccess:      true,
		StatusCode:     resp.StatusCode,
		LatencyMs:      latency,
		FailureDetails: diag,
	}
}
