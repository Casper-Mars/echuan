package api

import (
	"log/slog"
	"net/http"

	"github.com/Casper-Mars/echuan/internal/app"
	appv3 "github.com/Casper-Mars/echuan/internal/app/v3"
)

// V3Services is the dependency set of the V3 router. Products and materials
// are reused from the legacy services because their tables did not change
// shape; projects, tasks and workers are V3.
type V3Services struct {
	Products  *app.ProductService
	Materials *app.MaterialService
	Projects  *appv3.ProjectService
	Tasks     *appv3.Service
	Workers   *appv3.WorkerService
}

// routeSpecsV3 is the V3 route contract. Old v2 routes are deliberately absent:
// an unregistered /v1 path is answered with 404 by v1Fallback.
var routeSpecsV3 = []routeSpec{
	{http.MethodGet, "/healthz", ""},
	{http.MethodGet, "/readyz", ""},
	{http.MethodGet, "/version", ""},

	{http.MethodPost, "/v1/products", "products"},
	{http.MethodGet, "/v1/products", "products"},
	{http.MethodPatch, "/v1/products", "products"},
	{http.MethodDelete, "/v1/products", "products"},

	{http.MethodPost, "/v1/projects", "projects"},
	{http.MethodGet, "/v1/projects", "projects"},
	{http.MethodGet, "/v1/projects/materials", "projects"},
	{http.MethodPatch, "/v1/projects", "projects"},
	{http.MethodPost, "/v1/projects/close", "projects"},
	{http.MethodPost, "/v1/projects/reopen", "projects"},

	{http.MethodPost, "/v1/tasks", "tasks"},
	{http.MethodGet, "/v1/tasks", "tasks"},
	{http.MethodPatch, "/v1/tasks", "tasks"},
	{http.MethodDelete, "/v1/tasks", "tasks"},
	{http.MethodPost, "/v1/tasks/attach", "tasks"},
	{http.MethodPost, "/v1/tasks/claim", "tasks"},
	{http.MethodPost, "/v1/tasks/lease", "tasks"},
	{http.MethodPost, "/v1/tasks/report", "tasks"},
	{http.MethodPost, "/v1/tasks/decide", "tasks"},
	{http.MethodPost, "/v1/tasks/reset", "tasks"},
	{http.MethodGet, "/v1/tasks/events", "tasks"},
	{http.MethodGet, "/v1/tasks/dependencies", "tasks"},
	{http.MethodGet, "/v1/tasks/attachments", "tasks"},
	{http.MethodGet, "/v1/decisions", "tasks"},
	{http.MethodGet, "/v1/inbox", "tasks"},

	{http.MethodPost, "/v1/workers/heartbeat", "workers"},
	{http.MethodGet, "/v1/workers", "workers"},

	{http.MethodGet, "/v1/materials", "materials"},
	{http.MethodPost, "/v1/materials", "materials"},
}

// RoutesV3 returns every V3 method+path pair resolved by NewRouterV3.
func RoutesV3() []Route {
	routes := make([]Route, 0, len(routeSpecsV3))
	for _, spec := range routeSpecsV3 {
		routes = append(routes, Route{Method: spec.method, Path: spec.path})
	}
	return routes
}

// NewRouterV3 builds the V3 router. Resource routes are only registered when
// their service is non-nil, so health-only tests can build a router without a
// database.
func NewRouterV3(logger *slog.Logger, fallback http.Handler, checks []HealthChecker, services V3Services) http.Handler {
	h := NewHandler(logger, checks)

	handlers := map[string]http.HandlerFunc{
		"GET /healthz": h.healthz,
		"GET /readyz":  h.readyz,
		"GET /version": h.version,
	}
	enabled := map[string]bool{"": true}

	if services.Products != nil {
		enabled["products"] = true
		productsHandler := newProductsHandler(services.Products, logger)
		handlers["POST /v1/products"] = productsHandler.createProduct
		handlers["GET /v1/products"] = productsHandler.listOrGetProduct
		handlers["PATCH /v1/products"] = productsHandler.updateProduct
		handlers["DELETE /v1/products"] = productsHandler.deleteProduct
	}
	if services.Materials != nil {
		enabled["materials"] = true
		materialsHandler := newMaterialsHandler(services.Materials, logger)
		handlers["GET /v1/materials"] = materialsHandler.download
	}
	if services.Projects != nil {
		enabled["projects"] = true
	}
	if services.Workers != nil {
		enabled["workers"] = true
	}
	if services.Projects != nil || services.Tasks != nil || services.Workers != nil {
		v3Handler := newV3Handler(services, logger)
		enabled["tasks"] = services.Tasks != nil
		if services.Projects != nil {
			handlers["POST /v1/projects"] = v3Handler.createProject
			handlers["GET /v1/projects"] = v3Handler.listOrGetProject
			handlers["GET /v1/projects/materials"] = v3Handler.projectMaterials
			handlers["PATCH /v1/projects"] = v3Handler.updateProject
			handlers["POST /v1/projects/close"] = v3Handler.closeProject
			handlers["POST /v1/projects/reopen"] = v3Handler.reopenProject
		}
		if services.Tasks != nil {
			handlers["POST /v1/tasks"] = v3Handler.createTask
			handlers["GET /v1/tasks"] = v3Handler.listOrGetTask
			handlers["PATCH /v1/tasks"] = v3Handler.updateTask
			handlers["DELETE /v1/tasks"] = v3Handler.deleteTask
			handlers["POST /v1/tasks/attach"] = v3Handler.attach
			handlers["POST /v1/tasks/claim"] = v3Handler.claim
			handlers["POST /v1/tasks/lease"] = v3Handler.renewLease
			handlers["POST /v1/tasks/report"] = v3Handler.report
			handlers["POST /v1/tasks/decide"] = v3Handler.decide
			handlers["POST /v1/tasks/reset"] = v3Handler.reset
			handlers["GET /v1/tasks/events"] = v3Handler.events
			handlers["GET /v1/tasks/dependencies"] = v3Handler.dependencies
			handlers["GET /v1/tasks/attachments"] = v3Handler.attachments
			handlers["GET /v1/decisions"] = v3Handler.decisions
			handlers["GET /v1/inbox"] = v3Handler.inbox
		}
		if services.Workers != nil {
			handlers["POST /v1/workers/heartbeat"] = v3Handler.heartbeat
			handlers["GET /v1/workers"] = v3Handler.listOrGetWorker
		}
		if services.Materials != nil {
			handlers["POST /v1/materials"] = v3Handler.createMaterial
		}
	}

	mux := http.NewServeMux()
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
	for _, spec := range routeSpecsV3 {
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

	fallbackV1 := &v1Fallback{allowed: allowed}
	mux.HandleFunc("/v1/", fallbackV1.serve)
	mux.HandleFunc("/v1", fallbackV1.serve)

	if fallback != nil {
		mux.Handle("/", fallback)
	}
	return mux
}
