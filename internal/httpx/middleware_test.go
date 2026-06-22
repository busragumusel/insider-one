package httpx

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestAPIKeyAuthAllowsPublicRoutes(t *testing.T) {
	handler := WithAPIKeyAuth([]string{"secret"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestAPIKeyAuthRejectsProtectedRoutes(t *testing.T) {
	handler := WithAPIKeyAuth([]string{"secret"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/notifications", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}

func TestAPIKeyAuthAddsTenantContext(t *testing.T) {
	handler := WithAPIKeyAuth([]string{"secret"}, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := TenantID(r.Context()); got != "tenant-a" {
			t.Fatalf("unexpected tenant: %s", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodPost, "/notifications", nil)
	req.Header.Set("X-API-Key", "secret")
	req.Header.Set("X-Tenant-Id", "tenant-a")
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", rec.Code)
	}
}
