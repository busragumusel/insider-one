package docs

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSwaggerHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/swagger", nil)
	rec := httptest.NewRecorder()
	SwaggerHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "text/html") {
		t.Fatalf("unexpected content type: %s", contentType)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "SwaggerUIBundle") {
		t.Fatal("expected swagger ui html")
	}
}

func TestSpecHandler(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/openapi.yaml", nil)
	rec := httptest.NewRecorder()
	SpecHandler().ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
	if contentType := rec.Header().Get("Content-Type"); !strings.Contains(contentType, "application/yaml") {
		t.Fatalf("unexpected content type: %s", contentType)
	}
	body, _ := io.ReadAll(rec.Body)
	if !strings.Contains(string(body), "openapi: 3.0.3") {
		t.Fatal("expected openapi spec body")
	}
}
