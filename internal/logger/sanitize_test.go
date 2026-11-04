package logger

import (
	"testing"
)

func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Zulip Basic Auth URL",
			input:    "https://saga-ops-bot-bot%40zulip.sagasoft.xyz:dummy_zulip_bot_key_test123@zulip.sagasoft.xyz/api/v1/messages", // gitleaks:allow
			expected: "https://saga-ops-bot-bot%40zulip.sagasoft.xyz:******@zulip.sagasoft.xyz/api/v1/messages",
		},
		{
			name:     "Discord Webhook",
			input:    "https://discord.com/api/webhooks/123456789/abcdefgh12345",
			expected: "https://discord.com/api/webhooks/123456789/******",
		},
		{
			name:     "Slack Webhook",
			input:    "https://hooks.slack.com/services/T001/B002/secrettoken999",
			expected: "https://hooks.slack.com/services/T001/B002/******",
		},
		{
			name:     "Telegram Bot URL",
			input:    "https://api.telegram.org/bot123456:ABC-DEF1234ghIkl-zyx57W2v1u123ew11/sendMessage",
			expected: "https://api.telegram.org/bot******/sendMessage",
		},
		{
			name:     "Query Token URL",
			input:    "https://example.com/webhook?token=mysecret&channel=alerts",
			expected: "https://example.com/webhook?channel=alerts&token=******",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			actual := SanitizeURL(tc.input)
			if actual != tc.expected {
				t.Errorf("expected %q, got %q", tc.expected, actual)
			}
		})
	}
}
