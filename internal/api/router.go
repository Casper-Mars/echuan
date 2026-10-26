package api

import (
	"log/slog"
	"net/http"

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
//	POST /v1/projects/plans          submit a task breakdown (?id=)
//	POST /v1/projects/accept         draft → spec_review (?id=)
//	POST /v1/projects/decide         approve/reject a review gate (?id=, If-Match)
//	POST /v1/projects/cancel         cancel a non-terminal project (?id=)
//	POST /v1/tasks                    add one task to a planning project (?project_id=)
//	GET /v1/tasks                    list tasks (or fetch one with ?id=)
//	PATCH /v1/tasks                  patch title/body_md/priority (?id=)
//	DELETE /v1/tasks                 delete an unclaimed backlog task (?id=)
//	GET /v1/materials                download a material object (?id=)
//	GET /v1/projects/materials       version chain of one kind (?id=&kind=)
//	/                                fallback handler (embedded frontend), when provided
//
// Resource routes are only registered when their service is non-nil, which
// lets health-only tests build a router without a database.
//
// The returned handler is meant to be wrapped by Chain with the standard
// middleware stack.
func NewRouter(logger *slog.Logger, fallback http.Handler, checks []HealthChecker, products *app.ProductService, projects *app.ProjectService, tasks *app.TaskService, materials *app.MaterialService) http.Handler {
	h := NewHandler(logger, checks)

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /readyz", h.readyz)
	mux.HandleFunc("GET /version", h.version)
	if products != nil {
		productsHandler := newProductsHandler(products, logger)
		mux.HandleFunc("POST /v1/products", productsHandler.createProduct)
		mux.HandleFunc("GET /v1/products", productsHandler.listOrGetProduct)
		mux.HandleFunc("PATCH /v1/products", productsHandler.updateProduct)
		mux.HandleFunc("DELETE /v1/products", productsHandler.deleteProduct)
	}
	if projects != nil {
		projectsHandler := newProjectsHandler(projects, logger)
		mux.HandleFunc("POST /v1/projects", projectsHandler.createProject)
		mux.HandleFunc("GET /v1/projects", projectsHandler.listOrGetProject)
		mux.HandleFunc("PATCH /v1/projects", projectsHandler.updateProject)
		mux.HandleFunc("DELETE /v1/projects", projectsHandler.deleteProject)
		mux.HandleFunc("POST /v1/projects/requirement", projectsHandler.writeRequirement)
		mux.HandleFunc("POST /v1/projects/specs", projectsHandler.submitSpec)
		mux.HandleFunc("POST /v1/projects/plans", projectsHandler.submitPlan)
		mux.HandleFunc("POST /v1/projects/accept", projectsHandler.acceptProject)
		mux.HandleFunc("POST /v1/projects/decide", projectsHandler.decideProject)
		mux.HandleFunc("POST /v1/projects/cancel", projectsHandler.cancelProject)
	}
	if tasks != nil {
		tasksHandler := newTasksHandler(tasks, logger)
		mux.HandleFunc("POST /v1/tasks", tasksHandler.createTask)
		mux.HandleFunc("GET /v1/tasks", tasksHandler.listOrGetTask)
		mux.HandleFunc("PATCH /v1/tasks", tasksHandler.updateTask)
		mux.HandleFunc("DELETE /v1/tasks", tasksHandler.deleteTask)
	}
	if materials != nil {
		materialHandler := newMaterialsHandler(materials, logger)
		// "GET" also matches HEAD in a ServeMux pattern; the method-less
		// patterns catch every other verb so the 405 keeps the unified body.
		mux.HandleFunc("GET /v1/materials", materialHandler.download)
		mux.HandleFunc("/v1/materials", materialHandler.methodNotAllowed)
		mux.HandleFunc("GET /v1/projects/materials", materialHandler.chain)
		mux.HandleFunc("/v1/projects/materials", materialHandler.methodNotAllowed)
	}
	if fallback != nil {
		mux.Handle("/", fallback)
	}
	return mux
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
