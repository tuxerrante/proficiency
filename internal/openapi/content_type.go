package openapi

import "strings"

const contentTypeApplicationJSON = "application/json"

// IsJSONContentType reports whether a media type is JSON-compatible.
func IsJSONContentType(contentType string) bool {
	base := strings.TrimSpace(strings.ToLower(contentType))
	base = strings.SplitN(base, ";", 2)[0]

	return base == contentTypeApplicationJSON || strings.HasSuffix(base, "+json")
}
