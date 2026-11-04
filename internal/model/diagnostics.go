package model

// DiagnosticDetail holds granular diagnostic metrics and failure telemetry for admins.
type DiagnosticDetail struct {
	ProbePhase       string            `json:"probe_phase"`       // "DNS", "TCP_CONNECT", "TLS_HANDSHAKE", "HTTP_REQUEST", "SMTP_GREETING"
	DNSLookupMs      int64             `json:"dns_lookup_ms"`
	TCPConnectMs     int64             `json:"tcp_connect_ms"`
	TLSHandshakeMs   int64             `json:"tls_handshake_ms"`
	StatusCode       int               `json:"status_code,omitempty"`
	ExpectedCode     int               `json:"expected_code,omitempty"`
	CertIssuer       string            `json:"cert_issuer,omitempty"`
	CertExpiry       string            `json:"cert_expiry,omitempty"`
	CertExpiryDays   int               `json:"cert_expiry_days,omitempty"`
	ResponseHeaders  map[string]string `json:"response_headers,omitempty"`
	ResponseBodyHead string            `json:"response_body_head,omitempty"` // First 1KB of response body
	ErrorMessage     string            `json:"error_message,omitempty"`
}
