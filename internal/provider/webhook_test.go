package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestClientRoutesByChannel(t *testing.T) {
	var smsCalled, emailCalled bool
	smsServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		smsCalled = true
		writeProviderAccepted(t, w, "sms-message")
	}))
	defer smsServer.Close()
	emailServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		emailCalled = true
		writeProviderAccepted(t, w, "email-message")
	}))
	defer emailServer.Close()

	client := NewWithEndpoints(map[string]string{
		"sms":   smsServer.URL,
		"email": emailServer.URL,
	})
	resp, status, err := client.Send(context.Background(), DeliveryRequest{To: "+1", Channel: "sms", Content: "hello"})
	if err != nil {
		t.Fatal(err)
	}
	if status != http.StatusAccepted || resp.MessageID != "sms-message" {
		t.Fatalf("unexpected response: %+v status=%d", resp, status)
	}
	if !smsCalled || emailCalled {
		t.Fatalf("unexpected routing smsCalled=%v emailCalled=%v", smsCalled, emailCalled)
	}
}

func writeProviderAccepted(t *testing.T, w http.ResponseWriter, messageID string) {
	t.Helper()
	w.WriteHeader(http.StatusAccepted)
	err := json.NewEncoder(w).Encode(DeliveryResponse{
		MessageID: messageID,
		Status:    "accepted",
		Timestamp: time.Now().UTC(),
	})
	if err != nil {
		t.Fatal(err)
	}
}
