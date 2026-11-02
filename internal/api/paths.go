package api

import "strings"

// IsReservedPath reports whether p belongs to a path namespace owned by the
// API rather than by the embedded frontend: the /v1 prefix and the health and
// version endpoints.
//
// Case and trailing slashes are ignored on purpose. Variants such as
// "/healthz/", "/HEALTHZ" or "/version/" are client typos, not frontend
// routes: answering them with the SPA fallback would return 200 HTML and make
// a misconfigured probe look healthy, so the web handler must return 404
// instead.
func IsReservedPath(p string) bool {
	lower := strings.ToLower(strings.TrimRight(p, "/"))
	switch lower {
	case "/healthz", "/readyz", "/version":
		return true
	}
	return lower == "/v1" || strings.HasPrefix(lower, "/v1/")
}
