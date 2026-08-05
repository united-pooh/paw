package mcp

import "regexp"

var (
	authorizationBearerPattern = regexp.MustCompile(`(?i)(authorization[^\r\n]{0,48}?bearer\s+)([^\s,'"}\]]+)`)
	bareBearerPattern          = regexp.MustCompile(`(?i)(bearer\s+)([A-Za-z0-9._~+/=-]+)`)
)

func redactSensitiveText(value string) string {
	value = authorizationBearerPattern.ReplaceAllString(value, `${1}[REDACTED]`)
	return bareBearerPattern.ReplaceAllString(value, `${1}[REDACTED]`)
}
