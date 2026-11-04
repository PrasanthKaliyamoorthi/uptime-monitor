package checker

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"uptime-monitor/internal/model"
)

func TestHTTPProber(t *testing.T) {
	// Mock server verifying auth and custom keyword
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Service is Healthy"))
		case "/auth-bearer":
			auth := r.Header.Get("Authorization")
			if auth != "Bearer secret-token-123" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("Unauthorized Bearer"))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Authorized"))
		case "/auth-basic":
			user, pass, ok := r.BasicAuth()
			if !ok || user != "admin" || pass != "secret123" {
				w.WriteHeader(http.StatusUnauthorized)
				_, _ = w.Write([]byte("Unauthorized Basic"))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Basic OK"))
		case "/custom-header":
			val := r.Header.Get("X-Custom-Key")
			if val != "CustomVal" {
				w.WriteHeader(http.StatusBadRequest)
				_, _ = w.Write([]byte("Missing Header"))
				return
			}
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("Header OK"))
		case "/error":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("Internal Server Disaster"))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	prober := NewHTTPProber()
	ctx := context.Background()

	t.Run("Standard 200 OK", func(t *testing.T) {
		m := &model.Monitor{
			Target:       server.URL + "/ok",
			Method:       "GET",
			ExpectedCode: 200,
			Keyword:      "Healthy",
			TimeoutSec:   5,
		}
		res := prober.Probe(ctx, m)
		if !res.IsSuccess {
			t.Fatalf("expected success, got error: %s", res.ErrorMessage)
		}
		if res.StatusCode != 200 {
			t.Errorf("expected status 200, got %d", res.StatusCode)
		}
	})

	t.Run("Bearer Token Auth", func(t *testing.T) {
		m := &model.Monitor{
			Target:       server.URL + "/auth-bearer",
			Method:       "GET",
			AuthType:     "bearer",
			AuthToken:    "secret-token-123",
			ExpectedCode: 200,
			TimeoutSec:   5,
		}
		res := prober.Probe(ctx, m)
		if !res.IsSuccess {
			t.Fatalf("expected bearer auth success, got %s", res.ErrorMessage)
		}
	})

	t.Run("Basic Auth", func(t *testing.T) {
		m := &model.Monitor{
			Target:       server.URL + "/auth-basic",
			Method:       "GET",
			AuthType:     "basic",
			AuthUser:     "admin",
			AuthPass:     "secret123",
			ExpectedCode: 200,
			TimeoutSec:   5,
		}
		res := prober.Probe(ctx, m)
		if !res.IsSuccess {
			t.Fatalf("expected basic auth success, got %s", res.ErrorMessage)
		}
	})

	t.Run("Custom Request Headers", func(t *testing.T) {
		m := &model.Monitor{
			Target:       server.URL + "/custom-header",
			Method:       "GET",
			HeadersJSON:  `{"X-Custom-Key": "CustomVal"}`,
			ExpectedCode: 200,
			TimeoutSec:   5,
		}
		res := prober.Probe(ctx, m)
		if !res.IsSuccess {
			t.Fatalf("expected custom header success, got %s", res.ErrorMessage)
		}
	})

	t.Run("Status Code Mismatch & Response Snippet", func(t *testing.T) {
		m := &model.Monitor{
			Target:       server.URL + "/error",
			Method:       "GET",
			ExpectedCode: 200,
			TimeoutSec:   5,
		}
		res := prober.Probe(ctx, m)
		if res.IsSuccess {
			t.Fatalf("expected failure for 500 error, got success")
		}
		if res.ErrorType != "HTTP_CODE_MISMATCH" {
			t.Errorf("expected error type HTTP_CODE_MISMATCH, got %s", res.ErrorType)
		}
		if res.FailureDetails == nil || !strings.Contains(res.FailureDetails.ResponseBodyHead, "Internal Server Disaster") {
			t.Errorf("expected response body snippet to be captured, got %v", res.FailureDetails)
		}
	})

	t.Run("Keyword Missing", func(t *testing.T) {
		m := &model.Monitor{
			Target:       server.URL + "/ok",
			Method:       "GET",
			ExpectedCode: 200,
			Keyword:      "NonExistentKeyword",
			TimeoutSec:   5,
		}
		res := prober.Probe(ctx, m)
		if res.IsSuccess {
			t.Fatalf("expected failure when keyword is missing")
		}
		if res.ErrorType != "KEYWORD_MISSING" {
			t.Errorf("expected KEYWORD_MISSING, got %s", res.ErrorType)
		}
	})

	t.Run("Auto-Prefix Missing Scheme", func(t *testing.T) {
		bareURL := strings.TrimPrefix(server.URL, "http://")
		m := &model.Monitor{
			Target:       bareURL + "/ok",
			Protocol:     "http",
			Method:       "GET",
			ExpectedCode: 200,
			TimeoutSec:   5,
		}
		res := prober.Probe(ctx, m)
		if !res.IsSuccess {
			t.Fatalf("expected auto-prefixing http:// to succeed, got error: %s (%s)", res.ErrorMessage, res.ErrorType)
		}
	})

	t.Run("Untrusted TLS Detection", func(t *testing.T) {
		tlsServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		defer tlsServer.Close()

		m := &model.Monitor{
			Target:        tlsServer.URL,
			Protocol:      "https",
			SkipTLSVerify: false,
			TimeoutSec:    5,
		}
		res := prober.Probe(ctx, m)
		if res.IsSuccess {
			t.Fatalf("expected untrusted cert to fail when SkipTLSVerify is false")
		}
		if res.ErrorType != "CERT_UNTRUSTED" && res.ErrorType != "TLS_ERROR" {
			t.Errorf("expected CERT_UNTRUSTED or TLS_ERROR, got %s", res.ErrorType)
		}
	})
}
