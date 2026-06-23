package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"insider-one/internal/config"
	"insider-one/internal/docs"
	"insider-one/internal/domain"
	"insider-one/internal/httpx"
	"insider-one/internal/logging"
	"insider-one/internal/observability"
	"insider-one/internal/provider"
	"insider-one/internal/repository"
	"insider-one/internal/service"
	"insider-one/internal/telemetry"
	"insider-one/internal/worker"
)

type App struct {
	config    config.Config
	repo      *repository.Repository
	service   *service.Service
	telemetry *telemetry.Telemetry
	logger    *slog.Logger
	metrics   *observability.Metrics
	server    http.Handler
	cancel    context.CancelFunc
	startedAt time.Time
}

func New() (*App, error) {
	cfg := config.Load()
	logger, err := logging.New(cfg.LogLevel)
	if err != nil {
		return nil, err
	}
	return NewWithConfig(cfg, logger)
}

func NewWithConfig(cfg config.Config, logger *slog.Logger) (*App, error) {
	telemetryState, err := telemetry.New(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	repo, err := repository.New(cfg.DatabaseURL)
	if err != nil {
		return nil, err
	}
	if logger == nil {
		logger = slog.Default()
	}
	appCtx, cancel := context.WithCancel(context.Background())
	metrics := observability.NewMetrics()
	providerClient := provider.NewWithEndpoints(map[string]string{
		"default": cfg.ProviderURL,
		"sms":     cfg.SMSProviderURL,
		"email":   cfg.EmailProviderURL,
		"push":    cfg.PushProviderURL,
	})
	logger.Info("provider configured", "default_provider_url", cfg.ProviderURL, "sms_provider_url", cfg.SMSProviderURL, "email_provider_url", cfg.EmailProviderURL, "push_provider_url", cfg.PushProviderURL)
	serviceLayer := service.New(repo, providerClient, metrics, service.WithClaimLimit(cfg.WorkerClaimLimit), service.WithRateLimit(cfg.RateLimitPerSecond))
	app := &App{
		config:    cfg,
		repo:      repo,
		service:   serviceLayer,
		telemetry: telemetryState,
		logger:    logger,
		metrics:   metrics,
		cancel:    cancel,
		startedAt: time.Now().UTC(),
	}
	app.server = app.routes()
	worker.New(serviceLayer, app.logger, worker.WithConcurrency(cfg.WorkerConcurrency)).Start(appCtx)
	return app, nil
}

func (a *App) Close() error {
	if a.cancel != nil {
		a.cancel()
	}
	if a.telemetry != nil {
		_ = a.telemetry.Close(context.Background())
	}
	return a.repo.Close()
}

func (a *App) Router() http.Handler {
	return a.server
}

func (a *App) routes() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /swagger", docs.SwaggerHandler())
	mux.Handle("GET /openapi.yaml", docs.SpecHandler())
	mux.HandleFunc("GET /health", a.handleHealth)
	mux.HandleFunc("GET /metrics", a.handleMetrics)
	mux.HandleFunc("POST /notifications", a.handleCreateNotification)
	mux.HandleFunc("POST /notifications/batches", a.handleCreateBatch)
	mux.HandleFunc("GET /notifications/dead-letter", a.handleListDeadLetters)
	mux.HandleFunc("GET /notifications/{id}", a.handleGetNotification)
	mux.HandleFunc("GET /notifications/{id}/events", a.handleListNotificationEvents)
	mux.HandleFunc("GET /batches/{id}", a.handleGetBatch)
	mux.HandleFunc("PATCH /notifications/{id}/cancel", a.handleCancelOne)
	mux.HandleFunc("POST /notifications/cancel", a.handleCancelMany)
	mux.HandleFunc("GET /notifications", a.handleListNotifications)
	return httpx.WithRequestMetadata(a.logger, httpx.WithAPIKeyAuth(a.config.APIKeys, mux))
}

func (a *App) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, healthEnvelope{
		Status:   "ok",
		Service:  "insider-one-notifications",
		UptimeMs: time.Since(a.startedAt).Milliseconds(),
		Meta:     buildResponseMeta(r),
	})
}

func (a *App) handleMetrics(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, a.service.MetricsSnapshot(r.Context()))
}

func (a *App) handleCreateNotification(w http.ResponseWriter, r *http.Request) {
	var req domain.NotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err, r)
		return
	}
	corrID := httpx.CorrelationID(r.Context())
	if corrID == "" {
		corrID = correlationID(r)
	}
	idempotencyKey := r.Header.Get("Idempotency-Key")
	notification, err := a.service.CreateNotification(r.Context(), req, newID(), corrID, idempotencyKey)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err, r)
		return
	}
	writeJSON(w, http.StatusCreated, notificationEnvelope{Notification: notification, Meta: buildResponseMeta(r)})
}

func (a *App) handleCreateBatch(w http.ResponseWriter, r *http.Request) {
	var req domain.NotificationBatchRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err, r)
		return
	}
	if req.IdempotencyKey == "" {
		req.IdempotencyKey = r.Header.Get("Idempotency-Key")
	}
	corrID := httpx.CorrelationID(r.Context())
	if corrID == "" {
		corrID = correlationID(r)
	}
	batchID, notifications, err := a.service.CreateBatch(r.Context(), req, corrID)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err, r)
		return
	}
	writeJSON(w, http.StatusCreated, batchEnvelope{BatchID: batchID, Count: len(notifications), Notifications: notifications, Meta: buildResponseMeta(r)})
}

func (a *App) handleGetNotification(w http.ResponseWriter, r *http.Request) {
	notification, err := a.service.GetNotification(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err, r)
		return
	}
	writeJSON(w, http.StatusOK, notificationEnvelope{Notification: notification, Meta: buildResponseMeta(r)})
}

func (a *App) handleListNotificationEvents(w http.ResponseWriter, r *http.Request) {
	limit := boundedLimit(r, 100)
	offset := parseIntDefault(r.URL.Query().Get("offset"), 0)
	events, err := a.service.ListEvents(r.Context(), r.PathValue("id"), limit, offset)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err, r)
		return
	}
	writeJSON(w, http.StatusOK, eventListEnvelope{
		Events: events,
		Pagination: paginationMeta{
			Limit:    limit,
			Offset:   offset,
			Returned: len(events),
			HasMore:  len(events) == limit,
		},
		Meta: buildResponseMeta(r),
	})
}

func (a *App) handleGetBatch(w http.ResponseWriter, r *http.Request) {
	items, err := a.service.GetBatch(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", err, r)
		return
	}
	writeJSON(w, http.StatusOK, batchEnvelope{BatchID: r.PathValue("id"), Count: len(items), Notifications: items, Meta: buildResponseMeta(r)})
}

func (a *App) handleCancelOne(w http.ResponseWriter, r *http.Request) {
	count, err := a.service.Cancel(r.Context(), []string{r.PathValue("id")})
	if err != nil {
		writeError(w, http.StatusBadRequest, "cancel_failed", err, r)
		return
	}
	writeJSON(w, http.StatusOK, cancelEnvelope{Cancelled: count, IDs: []string{r.PathValue("id")}, Meta: buildResponseMeta(r)})
}

func (a *App) handleCancelMany(w http.ResponseWriter, r *http.Request) {
	var payload struct {
		IDs []string `json:"ids"`
	}
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", err, r)
		return
	}
	if len(payload.IDs) == 0 {
		writeError(w, http.StatusBadRequest, "validation_error", fmt.Errorf("ids array is required"), r)
		return
	}
	count, err := a.service.Cancel(r.Context(), payload.IDs)
	if err != nil {
		writeError(w, http.StatusBadRequest, "cancel_failed", err, r)
		return
	}
	writeJSON(w, http.StatusOK, cancelEnvelope{Cancelled: count, IDs: payload.IDs, Meta: buildResponseMeta(r)})
}

func (a *App) handleListNotifications(w http.ResponseWriter, r *http.Request) {
	limit := boundedLimit(r, 100)
	offset := parseIntDefault(r.URL.Query().Get("offset"), 0)
	from, _ := parseTimePtr(r.URL.Query().Get("from"))
	to, _ := parseTimePtr(r.URL.Query().Get("to"))
	items, err := a.service.List(r.Context(), r.URL.Query().Get("status"), r.URL.Query().Get("channel"), from, to, limit, offset)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err, r)
		return
	}
	writeJSON(w, http.StatusOK, listEnvelope{
		Notifications: items,
		Pagination: paginationMeta{
			Limit:    limit,
			Offset:   offset,
			Returned: len(items),
			HasMore:  len(items) == limit,
		},
		Meta: buildResponseMeta(r),
	})
}

func (a *App) handleListDeadLetters(w http.ResponseWriter, r *http.Request) {
	limit := boundedLimit(r, 100)
	offset := parseIntDefault(r.URL.Query().Get("offset"), 0)
	items, err := a.service.ListDeadLetters(r.Context(), r.URL.Query().Get("channel"), limit, offset)
	if err != nil {
		writeError(w, http.StatusBadRequest, "validation_error", err, r)
		return
	}
	writeJSON(w, http.StatusOK, listEnvelope{
		Notifications: items,
		Pagination: paginationMeta{
			Limit:    limit,
			Offset:   offset,
			Returned: len(items),
			HasMore:  len(items) == limit,
		},
		Meta: buildResponseMeta(r),
	})
}

func boundedLimit(r *http.Request, max int) int {
	limit := parseIntDefault(r.URL.Query().Get("limit"), 50)
	if limit <= 0 {
		return 50
	}
	if limit > max {
		return max
	}
	return limit
}

func parseIntDefault(value string, fallback int) int {
	if value == "" {
		return fallback
	}
	n, err := strconv.Atoi(value)
	if err != nil {
		return fallback
	}
	return n
}

func parseTimePtr(value string) (*time.Time, error) {
	if strings.TrimSpace(value) == "" {
		return nil, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	return &parsed, nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w http.ResponseWriter, status int, code string, err error, r *http.Request) {
	writeJSON(w, status, errorEnvelope{Error: err.Error(), Code: code, Meta: buildResponseMeta(r)})
}

func correlationID(r *http.Request) string {
	if value := r.Header.Get("X-Correlation-Id"); value != "" {
		return value
	}
	return fmt.Sprintf("corr-%d", time.Now().UnixNano())
}

func newID() string {
	return fmt.Sprintf("notif-%d", time.Now().UnixNano())
}
