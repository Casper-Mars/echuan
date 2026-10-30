package api

import (
	"log/slog"
	"net/http"
	"sort"
	"strings"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/platform"
)

// Handler serves the health and version endpoints.
type Handler struct {
	logger *slog.Logger
	checks []HealthChecker
}

// NewHandler builds a Handler. checks are evaluated by /readyz in order.
func NewHandler(logger *slog.Logger, checks []HealthChecker) *Handler {
	return &Handler{logger: logger, checks: checks}
}

// Route is one method+path pair served by the API.
type Route struct {
	Method string
	Path   string
}

// routeSpec is the single source of truth for routing: it drives both the
// ServeMux registration and the 405 Allow-list, and Routes exposes it so the
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

var routeSpecs = []routeSpec{
	{http.MethodGet, "/healthz", ""},
	{http.MethodGet, "/readyz", ""},
	{http.MethodGet, "/version", ""},

	{http.MethodPost, "/v1/products", "products"},
	{http.MethodGet, "/v1/products", "products"},
	{http.MethodPatch, "/v1/products", "products"},
	{http.MethodDelete, "/v1/products", "products"},

	{http.MethodPost, "/v1/projects", "projects"},
	{http.MethodGet, "/v1/projects", "projects"},
	{http.MethodPatch, "/v1/projects", "projects"},
	{http.MethodDelete, "/v1/projects", "projects"},
	{http.MethodPost, "/v1/projects/requirement", "projects"},
	{http.MethodPost, "/v1/projects/specs", "projects"},
	{http.MethodGet, "/v1/projects/package", "projects"},
	{http.MethodGet, "/v1/specs", "projects"},
	{http.MethodPost, "/v1/specs", "projects"},
	{http.MethodPatch, "/v1/specs", "projects"},
	{http.MethodPost, "/v1/projects/plans", "projects"},
	{http.MethodPost, "/v1/projects/submit", "projects"},
	{http.MethodPost, "/v1/projects/decide", "projects"},
	{http.MethodPost, "/v1/projects/cancel", "projects"},
	{http.MethodGet, "/v1/projects/materials", "materials"},

	{http.MethodPost, "/v1/tasks", "tasks"},
	{http.MethodGet, "/v1/tasks", "tasks"},
	{http.MethodPatch, "/v1/tasks", "tasks"},
	{http.MethodDelete, "/v1/tasks", "tasks"},
	{http.MethodPost, "/v1/tasks/report", "tasks"},
	{http.MethodPost, "/v1/tasks/decide", "tasks"},
	{http.MethodPost, "/v1/tasks/review", "tasks"},
	{http.MethodPost, "/v1/tasks/claim", "claims"},
	{http.MethodPost, "/v1/tasks/heartbeat", "claims"},

	{http.MethodPost, "/v1/workers/heartbeat", "workers"},
	{http.MethodGet, "/v1/workers", "workers"},

	{http.MethodGet, "/v1/materials", "materials"},
	{http.MethodGet, "/v1/inbox", "inbox"},
}

// Routes returns every method+path pair the full service set exposes. The
// contract tests compare it with the OpenAPI operation set, so adding a route
// in one place without the other fails the build.
func Routes() []Route {
	routes := make([]Route, 0, len(routeSpecs))
	for _, spec := range routeSpecs {
		routes = append(routes, Route{Method: spec.method, Path: spec.path})
	}
	return routes
}

// NewRouter builds the service router:
//
//	GET /healthz                     liveness
//	GET /readyz                      readiness
//	GET /version                     build information
//	POST /v1/products                create a product
//	GET /v1/products                 list products (or fetch one with ?id=)
//	PATCH /v1/products               patch a product (?id=)
//	DELETE /v1/products              delete a product (?id=)
//	POST /v1/projects                create a project (= requirement)
//	GET /v1/projects                 list projects (or fetch one with ?id=)
//	PATCH /v1/projects               rename a project (?id=)
//	DELETE /v1/projects              delete a draft/cancelled project (?id=)
//	POST /v1/projects/requirement    append a requirement version (?id=)
//	POST /v1/projects/specs          append a spec version (?id=)
//	GET /v1/projects/package         download every current document as a zip (?id=)
//	POST /v1/projects/plans          add a batch of task cards (?id=)
//	POST /v1/projects/submit         draft → its review gate (?id=)
//	POST /v1/projects/decide         approve/reject a review gate (?id=, If-Match)
//	POST /v1/projects/cancel         cancel a non-terminal project (?id=)
//	POST /v1/tasks                    add one task card to a tasks project (?project_id=)
//	GET /v1/tasks                    list tasks (or fetch one with ?id=)
//	PATCH /v1/tasks                  patch title/body_md/priority (?id=)
//	DELETE /v1/tasks                 delete an unclaimed backlog task (?id=)
//	POST /v1/tasks/claim             lease the next claimable task of a product
//	POST /v1/tasks/heartbeat         renew the lease of one task (?id=)
//	POST /v1/tasks/report            submit a report and complete the task (?id=)
//	POST /v1/tasks/decide            accept/rework/retry/abandon a task (?id=, If-Match)
//	POST /v1/tasks/review            approve/reject a task batch (?project_id=&decision=)
//	POST /v1/workers/heartbeat       register a worker, refresh it and dispatch work
//	GET /v1/workers                  list workers (or fetch one with ?id=)
//	GET /v1/materials                download a material object (?id=)
//	GET /v1/projects/materials       version chain of one kind (?id=&kind=)
//	GET /v1/inbox                    pending reviews and task decisions
//	/                                fallback handler (embedded frontend), when provided
//
// Resource routes are only registered when their service is non-nil, which
// lets health-only tests build a router without a database.
//
// The returned handler is meant to be wrapped by Chain with the standard
// middleware stack.
func NewRouter(logger *slog.Logger, fallback http.Handler, checks []HealthChecker, products *app.ProductService, projects *app.ProjectService, tasks *app.TaskService, claims *app.ClaimService, workers *app.WorkerService, materials *app.MaterialService, inbox *app.InboxService) http.Handler {
	h := NewHandler(logger, checks)

	// Handlers are collected per method+path; routeSpecs decides which of them
	// are reachable. A spec without a handler is a programmer error, caught
	// below instead of silently serving a 404.
	handlers := map[string]http.HandlerFunc{
		"GET /healthz": h.healthz,
		"GET /readyz":  h.readyz,
		"GET /version": h.version,
	}
	enabled := map[string]bool{"": true}

	if products != nil {
		enabled["products"] = true
		productsHandler := newProductsHandler(products, logger)
		handlers["POST /v1/products"] = productsHandler.createProduct
		handlers["GET /v1/products"] = productsHandler.listOrGetProduct
		handlers["PATCH /v1/products"] = productsHandler.updateProduct
		handlers["DELETE /v1/products"] = productsHandler.deleteProduct
	}
	if projects != nil {
		enabled["projects"] = true
		projectsHandler := newProjectsHandler(projects, logger)
		handlers["POST /v1/projects"] = projectsHandler.createProject
		handlers["GET /v1/projects"] = projectsHandler.listOrGetProject
		handlers["PATCH /v1/projects"] = projectsHandler.updateProject
		handlers["DELETE /v1/projects"] = projectsHandler.deleteProject
		handlers["POST /v1/projects/requirement"] = projectsHandler.writeRequirement
		handlers["POST /v1/projects/specs"] = projectsHandler.submitSpec
		handlers["GET /v1/projects/package"] = projectsHandler.downloadPackage
		handlers["GET /v1/specs"] = projectsHandler.listOrGetSpec
		handlers["POST /v1/specs"] = projectsHandler.saveSpecDocument
		handlers["PATCH /v1/specs"] = projectsHandler.saveSpecDocument
		handlers["POST /v1/projects/plans"] = projectsHandler.submitPlan
		handlers["POST /v1/projects/submit"] = projectsHandler.submitProject
		handlers["POST /v1/projects/decide"] = projectsHandler.decideProject
		handlers["POST /v1/projects/cancel"] = projectsHandler.cancelProject
	}
	if tasks != nil {
		enabled["tasks"] = true
		tasksHandler := newTasksHandler(tasks, logger)
		handlers["POST /v1/tasks"] = tasksHandler.createTask
		handlers["GET /v1/tasks"] = tasksHandler.listOrGetTask
		handlers["PATCH /v1/tasks"] = tasksHandler.updateTask
		handlers["DELETE /v1/tasks"] = tasksHandler.deleteTask
		handlers["POST /v1/tasks/report"] = tasksHandler.reportTask
		handlers["POST /v1/tasks/decide"] = tasksHandler.decideTask
		handlers["POST /v1/tasks/review"] = tasksHandler.reviewTasks
	}
	if claims != nil {
		enabled["claims"] = true
		claimsHandler := newClaimHandler(claims, logger)
		handlers["POST /v1/tasks/claim"] = claimsHandler.claim
		handlers["POST /v1/tasks/heartbeat"] = claimsHandler.heartbeat
	}
	if workers != nil {
		enabled["workers"] = true
		workersHandler := newWorkersHandler(workers, logger)
		handlers["POST /v1/workers/heartbeat"] = workersHandler.heartbeat
		handlers["GET /v1/workers"] = workersHandler.listOrGetWorker
	}
	if materials != nil {
		enabled["materials"] = true
		materialHandler := newMaterialsHandler(materials, logger)
		handlers["GET /v1/materials"] = materialHandler.download
		handlers["GET /v1/projects/materials"] = materialHandler.chain
	}
	if inbox != nil {
		enabled["inbox"] = true
		inboxHandler := newInboxHandler(inbox, logger)
		handlers["GET /v1/inbox"] = inboxHandler.list
	}

	mux := http.NewServeMux()
	// allowed maps a path to the methods it serves, including the implicit
	// HEAD that Go's mux derives from GET. It is the single Allow-list for the
	// /v1 fallback below.
	allowed := map[string]map[string]bool{}
	addAllowed := func(path, method string) {
		set := allowed[path]
		if set == nil {
			set = map[string]bool{}
			allowed[path] = set
		}
		set[method] = true
		if method == http.MethodGet {
			set[http.MethodHead] = true
		}
	}
	for _, spec := range routeSpecs {
		if !enabled[spec.service] {
			continue
		}
		key := spec.method + " " + spec.path
		handler, ok := handlers[key]
		if !ok {
			panic("api: route " + key + " has no handler")
		}
		mux.HandleFunc(key, handler)
		addAllowed(spec.path, spec.method)
	}

	// Catch every /v1 request that no method-specific pattern matched. Without
	// this, the SPA fallback registered at "/" would answer with its own
	// 405/404 semantics (and a truncated Allow header). The "/v1/" subtree is
	// more specific than "/", and "/v1" covers the bare prefix, which Go would
	// otherwise redirect to "/v1/".
	fallbackV1 := &v1Fallback{allowed: allowed}
	mux.HandleFunc("/v1/", fallbackV1.serve)
	mux.HandleFunc("/v1", fallbackV1.serve)

	if fallback != nil {
		mux.Handle("/", fallback)
	}
	return mux
}

// v1Fallback answers /v1 requests that did not match a registered route. It
// keeps the API's 404/405 semantics instead of delegating to the frontend:
//
//   - unknown path            → 404 not_found (for every method)
//   - known path, wrong verb  → 405 method_not_allowed with the full Allow list
//   - known path, right verb  → 405 as well: reaching the fallback with an
//     allowed method means a handler is missing from the router, and a 405 is
//     easier to spot than a silent 404.
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

type versionResponse struct {
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	GoVersion string `json:"go_version"`
}

// version reports the build identity injected at link time.
func (h *Handler) version(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, versionResponse{
		Version:   platform.Version,
		Commit:    platform.Commit,
		GoVersion: platform.GoVersion(),
	})
}
