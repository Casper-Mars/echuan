package api

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// doJSONHeaders issues a request with extra headers. It is the general form the
// remaining HTTP tests use for If-Match and Location assertions.
func doJSONHeaders(t *testing.T, h http.Handler, method, target string, body any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()

	var reader io.Reader
	switch value := body.(type) {
	case nil:
	case string:
		reader = strings.NewReader(value)
	case io.Reader:
		reader = value
	default:
		payload, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(payload)
	}

	req := httptest.NewRequest(method, target, reader)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(HeaderActor, "test-suite")
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// decodeProjectFromResponse decodes one project body (a decide/transition
// response, not the detail form).
func decodeProjectFromResponse(t *testing.T, rec *httptest.ResponseRecorder) projectResponse {
	t.Helper()
	var project projectResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &project); err != nil {
		t.Fatalf("decode project %q: %v", rec.Body.String(), err)
	}
	return project
}
