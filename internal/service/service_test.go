package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"insider-one/internal/domain"
	"insider-one/internal/observability"
	"insider-one/internal/provider"
	"insider-one/internal/repository"
	"insider-one/internal/testdb"
)

func TestCreateNotificationValidation(t *testing.T) {
	service := New(nil, provider.New("http://example.invalid"), observability.NewMetrics())
	_, err := service.CreateNotification(context.Background(), domain.NotificationRequest{Channel: domain.ChannelSMS, Content: "hi"}, "batch", "corr", "")
	if err != nil {
		return
	}
	t.Fatal("expected recipient validation error")
}

func TestProviderSend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"messageId":"abc","status":"accepted","timestamp":"2026-06-22T00:00:00Z"}`))
	}))
	defer server.Close()

	client := provider.New(server.URL)
	resp, status, err := client.Send(context.Background(), provider.DeliveryRequest{To: "+1", Channel: "sms", Content: "hello"})
	if err != nil || status != http.StatusAccepted || resp.MessageID != "abc" {
		t.Fatalf("unexpected response: %+v status=%d err=%v", resp, status, err)
	}
}

func TestProcessChannelRecordsPermanentFailureEvent(t *testing.T) {
	repo := openTestRepository(t)
	defer repo.Close()

	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"messageId":"","status":"bad_request","timestamp":"2026-06-22T00:00:00Z"}`))
	}))
	defer providerServer.Close()

	service := New(repo, provider.New(providerServer.URL), observability.NewMetrics(), WithClaimLimit(25), WithRateLimit(1000))
	notification, err := service.CreateNotification(context.Background(), domain.NotificationRequest{
		Recipient: "+905551234567",
		Channel:   domain.ChannelSMS,
		Content:   "hello",
		Priority:  domain.PriorityNormal,
	}, "batch-failed", "corr", "")
	if err != nil {
		t.Fatal(err)
	}

	got := processUntilStatus(t, service, notification.ID, domain.StatusFailed)
	if got.Status != domain.StatusFailed {
		t.Fatalf("expected failed status, got %s", got.Status)
	}
	if got.Attempts != 1 {
		t.Fatalf("expected one attempt, got %d", got.Attempts)
	}

	events, err := service.ListEvents(context.Background(), notification.ID, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("expected accepted and failed events, got %d", len(events))
	}
	if events[1].Status != domain.StatusFailed || events[1].ProviderStatus != http.StatusBadRequest {
		t.Fatalf("unexpected failure event: %+v", events[1])
	}
}

func TestProcessChannelMovesExhaustedRetryToDeadLetter(t *testing.T) {
	repo := openTestRepository(t)
	defer repo.Close()

	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"messageId":"","status":"rate_limited","timestamp":"2026-06-22T00:00:00Z"}`))
	}))
	defer providerServer.Close()

	now := time.Now().UTC()
	notification := domain.Notification{
		ID:          fmt.Sprintf("exhausted-retry-%d", now.UnixNano()),
		BatchID:     "batch-dead-letter",
		Recipient:   "+905551234567",
		Channel:     domain.ChannelSMS,
		Content:     "hello",
		Priority:    domain.PriorityNormal,
		Status:      domain.StatusPending,
		Attempts:    2,
		MaxAttempts: 3,
		CreatedAt:   now,
		UpdatedAt:   now,
	}
	if err := repo.InsertNotifications(context.Background(), []domain.Notification{notification}); err != nil {
		t.Fatal(err)
	}

	service := New(repo, provider.New(providerServer.URL), observability.NewMetrics(), WithClaimLimit(25), WithRateLimit(1000))
	got := processUntilStatus(t, service, notification.ID, domain.StatusDeadLetter)
	if got.Status != domain.StatusDeadLetter {
		t.Fatalf("expected dead_letter status, got %s", got.Status)
	}
	if got.Attempts != 3 {
		t.Fatalf("expected three attempts, got %d", got.Attempts)
	}

	deadLetters, err := service.ListDeadLetters(context.Background(), string(domain.ChannelSMS), 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !containsNotification(deadLetters, notification.ID) {
		t.Fatalf("unexpected dead letters: %+v", deadLetters)
	}
}

func BenchmarkCreateBatchHighThroughput(b *testing.B) {
	databaseURL := os.Getenv("TEST_DATABASE_URL")
	if databaseURL == "" {
		b.Skip("TEST_DATABASE_URL is required for Postgres benchmark")
	}
	repo, err := repository.New(databaseURL)
	if err != nil {
		b.Fatal(err)
	}
	defer repo.Close()

	providerServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"messageId":"abc","status":"accepted","timestamp":"2026-06-22T00:00:00Z"}`))
	}))
	defer providerServer.Close()

	service := New(repo, provider.New(providerServer.URL), observability.NewMetrics())
	ctx := context.Background()
	request := domain.NotificationBatchRequest{Notifications: make([]domain.NotificationRequest, 1000)}
	for i := range request.Notifications {
		request.Notifications[i] = domain.NotificationRequest{
			Recipient: fmt.Sprintf("+90555%07d", i),
			Channel:   domain.ChannelSMS,
			Content:   "benchmark payload",
			Priority:  domain.PriorityNormal,
		}
	}

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		batchRequest := request
		batchRequest.IdempotencyKey = fmt.Sprintf("bench-batch-%d-%d", time.Now().UnixNano(), i)
		if _, _, err := service.CreateBatch(ctx, batchRequest, "bench-corr"); err != nil {
			b.Fatal(err)
		}
	}
}

func openTestRepository(t *testing.T) *repository.Repository {
	t.Helper()
	databaseURL := testdb.URL(t)
	repo, err := repository.New(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	return repo
}

func containsNotification(items []domain.Notification, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func processUntilStatus(t *testing.T, service *Service, id string, status domain.Status) domain.Notification {
	t.Helper()
	var last domain.Notification
	for i := 0; i < 50; i++ {
		if err := service.ProcessChannel(context.Background(), string(domain.ChannelSMS)); err != nil {
			t.Fatal(err)
		}
		got, err := service.GetNotification(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		last = got
		if got.Status == status {
			return got
		}
		time.Sleep(20 * time.Millisecond)
	}
	return last
}
