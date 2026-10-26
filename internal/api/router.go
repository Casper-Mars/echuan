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
//	GET /healthz            liveness
//	GET /readyz             readiness
//	GET /version            build information
//	POST /v1/products       create a product
//	GET /v1/products        list products (or fetch one with ?id=)
//	PATCH /v1/products      patch a product (?id=)
//	DELETE /v1/products     delete a product (?id=)
//	/                       fallback handler (embedded frontend), when provided
//
// Product routes are only registered when products is non-nil, which lets
// health-only tests build a router without a database.
//
// The returned handler is meant to be wrapped by Chain with the standard
// middleware stack.
func NewRouter(logger *slog.Logger, fallback http.Handler, checks []HealthChecker, products *app.ProductService) http.Handler {
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
