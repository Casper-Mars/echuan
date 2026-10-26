package api

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Casper-Mars/echuan/internal/app"
	"github.com/Casper-Mars/echuan/internal/domain"
)

// maxRequestBodyBytes bounds request bodies so a malformed client cannot make
// the service allocate without limit.
const maxRequestBodyBytes = 1 << 20

// defaultProductListLimit is applied when ?limit= is absent.
const defaultProductListLimit = 50

// productsHandler serves /v1/products.
type productsHandler struct {
	service *app.ProductService
	logger  *slog.Logger
}

func newProductsHandler(service *app.ProductService, logger *slog.Logger) *productsHandler {
	return &productsHandler{service: service, logger: logger}
}

// createProductRequest is the POST body. "id" and other unknown members are
// rejected by the decoder; product_id is accepted only at creation.
type createProductRequest struct {
	ProductID   string  `json:"product_id"`
	Name        string  `json:"name"`
	GitURL      string  `json:"git_url"`
	Description *string `json:"description"`
	License     *string `json:"license"`
}

// updateProductRequest is the PATCH body. Every field is optional; ProductID
// is decoded so that attempting to change it yields a precise field error
// instead of a generic "unknown field".
type updateProductRequest struct {
	ProductID   *string `json:"product_id"`
	Name        *string `json:"name"`
	GitURL      *string `json:"git_url"`
	Description *string `json:"description"`
	License     *string `json:"license"`
}

// productResponse is the wire representation of a product.
type productResponse struct {
	ID           string `json:"id"`
	ProductID    string `json:"product_id"`
	Name         string `json:"name"`
	GitURL       string `json:"git_url"`
	Description  string `json:"description"`
	License      string `json:"license"`
	ProjectCount int64  `json:"project_count"`
	CreateTime   string `json:"create_time"`
	UpdateTime   string `json:"update_time"`
}

// productListResponse is the list envelope; next_cursor is omitted on the
// last page.
type productListResponse struct {
	Items      []productResponse `json:"items"`
	NextCursor *string           `json:"next_cursor,omitempty"`
}

// createProduct handles POST /v1/products.
func (h *productsHandler) createProduct(w http.ResponseWriter, r *http.Request) {
	var req createProductRequest
	if verr := decodeJSONBody(w, r, &req); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	product, err := h.service.CreateProduct(r.Context(), app.CreateProductInput{
		ProductID:   req.ProductID,
		Name:        req.Name,
		GitURL:      req.GitURL,
		Description: derefString(req.Description),
		License:     derefString(req.License),
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	WithSubject(r.Context(), product.ProductID)
	w.Header().Set("Location", "/v1/products?id="+url.QueryEscape(product.ProductID))
	writeJSON(w, http.StatusCreated, toProductResponse(product))
}

// listOrGetProduct handles GET /v1/products: ?id= selects the detail form,
// otherwise the collection is listed with limit/cursor.
func (h *productsHandler) listOrGetProduct(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if query.Has("id") {
		h.getProduct(w, r, query.Get("id"))
		return
	}
	h.listProducts(w, r)
}

// getProduct handles GET /v1/products?id=APP.
func (h *productsHandler) getProduct(w http.ResponseWriter, r *http.Request, productID string) {
	if strings.TrimSpace(productID) == "" {
		writeDomainError(w, r, h.logger, &domain.ValidationError{Fields: []domain.FieldError{
			{Field: "id", Reason: "is required"},
		}})
		return
	}
	WithSubject(r.Context(), productID)

	product, err := h.service.GetProduct(r.Context(), productID)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, toProductResponse(product))
}

// listProducts handles GET /v1/products?limit=&cursor=.
func (h *productsHandler) listProducts(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()

	limit := defaultProductListLimit
	if query.Has("limit") {
		parsed, err := strconv.Atoi(query.Get("limit"))
		if err != nil {
			writeDomainError(w, r, h.logger, invalidField("limit", "must be an integer"))
			return
		}
		limit = parsed
	}

	var afterID int64
	if query.Has("cursor") {
		id, err := decodeCursor(query.Get("cursor"))
		if err != nil {
			writeDomainError(w, r, h.logger, invalidField("cursor", "is invalid"))
			return
		}
		afterID = id
	}

	page, err := h.service.ListProducts(r.Context(), afterID, limit)
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}

	items := make([]productResponse, 0, len(page.Items))
	for _, product := range page.Items {
		items = append(items, toProductResponse(product))
	}
	response := productListResponse{Items: items}
	if page.NextID != 0 {
		next := encodeCursor(page.NextID)
		response.NextCursor = &next
	}
	writeJSON(w, http.StatusOK, response)
}

// updateProduct handles PATCH /v1/products?id=APP.
func (h *productsHandler) updateProduct(w http.ResponseWriter, r *http.Request) {
	productID := r.URL.Query().Get("id")
	if strings.TrimSpace(productID) == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), productID)

	var req updateProductRequest
	if verr := decodeJSONBody(w, r, &req); verr != nil {
		writeDomainError(w, r, h.logger, verr)
		return
	}

	product, err := h.service.UpdateProduct(r.Context(), productID, app.UpdateProductInput{
		ProductID:   req.ProductID,
		Name:        req.Name,
		GitURL:      req.GitURL,
		Description: req.Description,
		License:     req.License,
	})
	if err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	writeJSON(w, http.StatusOK, toProductResponse(product))
}

// deleteProduct handles DELETE /v1/products?id=APP.
func (h *productsHandler) deleteProduct(w http.ResponseWriter, r *http.Request) {
	productID := r.URL.Query().Get("id")
	if strings.TrimSpace(productID) == "" {
		writeDomainError(w, r, h.logger, invalidField("id", "is required"))
		return
	}
	WithSubject(r.Context(), productID)

	if err := h.service.DeleteProduct(r.Context(), productID); err != nil {
		writeDomainError(w, r, h.logger, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// toProductResponse maps the domain entity to the wire DTO.
func toProductResponse(p domain.Product) productResponse {
	return productResponse{
		ID:           p.ProductID,
		ProductID:    p.ProductID,
		Name:         p.Name,
		GitURL:       p.GitURL,
		Description:  p.Description,
		License:      p.License,
		ProjectCount: p.ProjectCount,
		CreateTime:   p.CreateTime.UTC().Format(time.RFC3339Nano),
		UpdateTime:   p.UpdateTime.UTC().Format(time.RFC3339Nano),
	}
}

// decodeJSONBody decodes exactly one JSON object, rejecting unknown fields and
// trailing data. Every failure is a field-level validation error.
func decodeJSONBody(w http.ResponseWriter, r *http.Request, dst any) *domain.ValidationError {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxRequestBodyBytes))
	decoder.DisallowUnknownFields()

	if err := decoder.Decode(dst); err != nil {
		return bodyDecodeError(err)
	}
	if err := decoder.Decode(&json.RawMessage{}); !errors.Is(err, io.EOF) {
		return invalidField("body", "unexpected trailing data")
	}
	return nil
}

// bodyDecodeError translates encoding/json failures into field errors.
func bodyDecodeError(err error) *domain.ValidationError {
	var typeErr *json.UnmarshalTypeError
	if errors.As(err, &typeErr) {
		field := typeErr.Field
		if field == "" {
			field = "body"
		}
		return invalidField(field, "has the wrong type")
	}

	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		return invalidField("body", "request body is too large")
	}
	if errors.Is(err, io.EOF) {
		return invalidField("body", "request body is required")
	}

	const unknownPrefix = `json: unknown field "`
	if strings.HasPrefix(err.Error(), unknownPrefix) {
		rest := strings.TrimPrefix(err.Error(), unknownPrefix)
		if i := strings.IndexByte(rest, '"'); i > 0 {
			return invalidField(rest[:i], "unknown field")
		}
	}
	return invalidField("body", "invalid JSON")
}

// invalidField builds a one-field validation error.
func invalidField(field, reason string) *domain.ValidationError {
	return &domain.ValidationError{Fields: []domain.FieldError{{Field: field, Reason: reason}}}
}

// derefString turns an optional JSON string into its value.
func derefString(v *string) string {
	if v == nil {
		return ""
	}
	return *v
}

// encodeCursor renders the opaque cursor as base64url("id:<physical id>").
func encodeCursor(id int64) string {
	raw := "id:" + strconv.FormatInt(id, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// decodeCursor parses the opaque cursor back into the last physical id.
func decodeCursor(cursor string) (int64, error) {
	if cursor == "" {
		return 0, errors.New("empty cursor")
	}
	decoded, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, err
	}
	const prefix = "id:"
	value := string(decoded)
	if !strings.HasPrefix(value, prefix) {
		return 0, errors.New("cursor is not an id cursor")
	}
	id, err := strconv.ParseInt(strings.TrimPrefix(value, prefix), 10, 64)
	if err != nil {
		return 0, err
	}
	if id < 0 {
		return 0, errors.New("cursor id is negative")
	}
	return id, nil
}
