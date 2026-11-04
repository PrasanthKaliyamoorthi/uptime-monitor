package logger

import (
	"net/url"
	"regexp"
	"strings"
)

var (
	discordWebhookRegex = regexp.MustCompile(`(https?://(?:ptb\.|canary\.)?discord(?:app)?\.com/api/webhooks/\d+/)[^/?#]+`)
	slackWebhookRegex   = regexp.MustCompile(`(https?://hooks\.slack\.com/services/T[A-Z0-9]+/B[A-Z0-9]+/)[^/?#]+`)
	telegramBotRegex    = regexp.MustCompile(`(https?://api\.telegram\.org/bot)[^/]+(/.*)`)
)

// SanitizeURL strips or redacts passwords, tokens, API keys, and sensitive query params from a URL string
func SanitizeURL(rawURL string) string {
	if rawURL == "" {
		return ""
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		// Fallback regex masking
		return maskKnownTokens(rawURL)
	}

	// Mask userinfo password
	if u.User != nil {
		if _, hasPass := u.User.Password(); hasPass {
			u.User = url.UserPassword(u.User.Username(), "******")
		}
	}

	// Mask sensitive query parameters
	if u.RawQuery != "" {
		q := u.Query()
		sensitiveKeys := []string{"token", "secret", "key", "api_key", "apikey", "auth", "password", "access_token"}
		modified := false
		for _, k := range sensitiveKeys {
			for queryKey := range q {
				if strings.EqualFold(queryKey, k) {
					q.Set(queryKey, "******")
					modified = true
				}
			}
		}
		if modified {
			u.RawQuery = q.Encode()
		}
	}

	sanitized := u.String()
	sanitized = strings.ReplaceAll(sanitized, "%2A%2A%2A%2A%2A%2A", "******")
	return maskKnownTokens(sanitized)
}

func maskKnownTokens(str string) string {
	// Discord
	str = discordWebhookRegex.ReplaceAllString(str, "${1}******")
	// Slack
	str = slackWebhookRegex.ReplaceAllString(str, "${1}******")
	// Telegram
	str = telegramBotRegex.ReplaceAllString(str, "${1}******${2}")
	return str
}
