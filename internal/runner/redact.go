package runner

import "regexp"

var secretPatterns = []*regexp.Regexp{
	// Credentialed URLs (postgres://user:pass@host, redis://:pass@host, http(s)://user:pass@host)
	regexp.MustCompile(`(?i)\b((?:postgres(?:ql)?|rediss?|https?|amqp|mysql)://)([^/\s:@]*):([^@\s]+)@`),
	// Bearer / Basic auth headers or tokens
	regexp.MustCompile(`(?i)\b(Bearer|Basic)\s+[A-Za-z0-9._~+/=-]{8,}`),
	// Polar / AFC license keys
	regexp.MustCompile(`\bAFC_(?:PRO|TEAM)_[A-Za-z0-9_-]{8,}`),
	// Common API keys & GitHub tokens
	regexp.MustCompile(`\b(?:sk-[A-Za-z0-9_-]{16,}|gh[pousr]_[A-Za-z0-9]{16,}|github_pat_[A-Za-z0-9_]{16,})`),
	// Sensitive KEY=VALUE assignments
	regexp.MustCompile(`(?i)\b([A-Z0-9_]*(?:PASSWORD|SECRET|TOKEN|API_KEY|LICENSE_KEY|DSN)[A-Z0-9_]*)=([^\s"']+|"[^"]*"|'[^']*')`),
}

// RedactSecrets scrubs common credential patterns from child process output
// before embedding errors in console, SARIF, or JUnit reports.
func RedactSecrets(s string) string {
	if s == "" {
		return ""
	}
	out := secretPatterns[0].ReplaceAllString(s, `${1}${2}:[REDACTED]@`)
	out = secretPatterns[1].ReplaceAllString(out, `${1} [REDACTED]`)
	out = secretPatterns[2].ReplaceAllString(out, `[REDACTED_LICENSE_KEY]`)
	out = secretPatterns[3].ReplaceAllString(out, `[REDACTED_TOKEN]`)
	out = secretPatterns[4].ReplaceAllString(out, `${1}=[REDACTED]`)
	return out
}
