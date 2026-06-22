package app

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"insider-one/internal/domain"
	"insider-one/internal/testdb"
)

func TestAPIResponseEnvelope(t *testing.T) {
	databaseURL := testdb.URL(t)

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"messageId":"prov-1","status":"accepted","timestamp":"2026-06-22T00:00:00Z"}`))
	}))
	defer provider.Close()

	if err := os.Setenv("DATABASE_URL", databaseURL); err != nil {
		t.Fatal(err)
	}
	if err := os.Setenv("PROVIDER_URL", provider.URL); err != nil {
		t.Fatal(err)
	}
	defer os.Unsetenv("DATABASE_URL")
	defer os.Unsetenv("PROVIDER_URL")

	application, err := New()
	if err != nil {
		t.Fatal(err)
	}
	defer application.Close()

	server := httptest.NewServer(application.Router())
	defer server.Close()

	body := `{"recipient":"+905551234567","channel":"sms","content":"hello","priority":"high"}`
	resp, err := http.Post(server.URL+"/notifications", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}

	var payload struct {
		Notification domain.Notification `json:"notification"`
		Meta         map[string]any      `json:"meta"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload.Notification.Recipient != "+905551234567" {
		t.Fatalf("unexpected recipient: %s", payload.Notification.Recipient)
	}
	if _, ok := payload.Meta["requestId"]; !ok {
		t.Fatal("missing request metadata")
	}
	if _, ok := payload.Meta["correlationId"]; !ok {
		t.Fatal("missing correlation metadata")
	}
}
