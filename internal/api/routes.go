package api

import (
	"net/http"
	"sort"
	"strings"
)

// Route is one method+path pair exposed by the router contract.
type Route struct {
	Method string
	Path   string
}

// routeSpec is the single source of truth for V3 routing: it drives both the
// ServeMux registration and the 405 Allow-list, and RoutesV3 exposes it so the
// contract tests can keep it in sync with api/openapi.yaml.
//
// service names the dependency that must be configured for the route to be
// registered. The empty service marks a route that is always available
// (health and version).
type routeSpec struct {
	method  string
	path    string
	service string
}

// v1Fallback answers unregistered paths. A method mismatch returns 405 with an
// Allow header: a 405 means a handler is missing from the router, and a 405 is
// easier to spot than a silent 404.
type v1Fallback struct {
	allowed map[string]map[string]bool
}

func (f *v1Fallback) serve(w http.ResponseWriter, r *http.Request) {
	methods, known := f.allowed[r.URL.Path]
	if !known {
		writeError(w, r, http.StatusNotFound, "not_found", "not found", nil)
		return
	}
	allow := make([]string, 0, len(methods))
	for method := range methods {
		allow = append(allow, method)
	}
	sort.Strings(allow)
	w.Header().Set("Allow", strings.Join(allow, ", "))
	writeError(w, r, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed", nil)
}
