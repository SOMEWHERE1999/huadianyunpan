package auth

import "strings"

// Redact replaces sensitive token/cookie values with a masked form.
func Redact(s string) string {
	if len(s) <= 8 {
		return "***"
	}
	return s[:4] + "..." + s[len(s)-4:]
}

// RedactCookies returns a safe representation of cookie names for logging.
func RedactCookies(cookies []StoredCookie) string {
	names := make([]string, len(cookies))
	for i, c := range cookies {
		names[i] = c.Name
	}
	return "[" + strings.Join(names, ", ") + "]"
}

// RedactToken returns a safe representation of an access token.
func RedactToken(token string) string {
	if token == "" {
		return "<empty>"
	}
	return Redact(token)
}
