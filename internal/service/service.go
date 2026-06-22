package service

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"insider-one/internal/domain"
	"insider-one/internal/httpx"
	"insider-one/internal/observability"
	"insider-one/internal/provider"
	"insider-one/internal/repository"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("insider-one/service")

type Service struct {
	repo       *repository.Repository
	provider   *provider.Client
	metrics    *observability.Metrics
	claimLimit int
	rateLimit  int
}

type Option func(*Service)

func WithClaimLimit(limit int) Option {
	return func(s *Service) {
		if limit > 0 {
			s.claimLimit = limit
		}
	}
}

func WithRateLimit(limit int) Option {
	return func(s *Service) {
		if limit > 0 {
			s.rateLimit = limit
		}
	}
}

func New(repo *repository.Repository, provider *provider.Client, metrics *observability.Metrics, opts ...Option) *Service {
	s := &Service{repo: repo, provider: provider, metrics: metrics, claimLimit: 10, rateLimit: 100}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

func (s *Service) CreateNotification(ctx context.Context, req domain.NotificationRequest, batchID, correlationID, idempotencyKey string) (domain.Notification, error) {
	ctx, span := tracer.Start(ctx, "service.create_notification",
		trace.WithAttributes(
			attribute.String("notification.channel", string(req.Channel)),
			attribute.String("notification.priority", string(req.Priority)),
			attribute.String("tenant.id", httpx.TenantID(ctx)),
		),
	)
	defer span.End()
	if err := ctx.Err(); err != nil {
		recordSpanError(span, err)
		return domain.Notification{}, err
	}
	if err := validateRequest(req); err != nil {
		recordSpanError(span, err)
		return domain.Notification{}, err
	}
	logger := httpx.Logger(ctx)
	tenantID := httpx.TenantID(ctx)
	if idempotencyKey != "" {
		if existing, err := s.repo.GetByIdempotencyKey(ctx, idempotencyKey, tenantID); err == nil {
			if logger != nil {
				logger.Info("notification request deduplicated", "notification_id", existing.ID, "batch_id", existing.BatchID)
			}
			return existing, nil
		}
	}
	now := time.Now().UTC()
	status := domain.StatusPending
	if req.ScheduledAt != nil && req.ScheduledAt.After(now) {
		status = domain.StatusScheduled
	}
	n := domain.Notification{
		ID:             newID(),
		TenantID:       tenantID,
		BatchID:        batchID,
		Recipient:      req.Recipient,
		Channel:        req.Channel,
		Content:        req.Content,
		Priority:       normalizePriority(req.Priority),
		Status:         status,
		Attempts:       0,
		MaxAttempts:    3,
		ScheduledAt:    req.ScheduledAt,
		CreatedAt:      now,
		UpdatedAt:      now,
		CorrelationID:  correlationID,
		IdempotencyKey: idempotencyKey,
	}
	if err := s.repo.InsertNotifications(ctx, []domain.Notification{n}); err != nil {
		if idempotencyKey != "" && strings.Contains(strings.ToLower(err.Error()), "unique constraint failed") {
			if existing, lookupErr := s.repo.GetByIdempotencyKey(ctx, idempotencyKey, tenantID); lookupErr == nil {
				if logger != nil {
					logger.Info("notification request deduplicated after insert race", "notification_id", existing.ID, "batch_id", existing.BatchID)
				}
				return existing, nil
			}
		}
		recordSpanError(span, err)
		return domain.Notification{}, err
	}
	if idempotencyKey != "" {
		_ = s.repo.UpsertRequestMapping(ctx, idempotencyKey, batchID, tenantID)
	}
	if logger != nil {
		logger.Info("notification created", "notification_id", n.ID, "batch_id", batchID, "channel", n.Channel, "status", n.Status)
	}
	s.recordEvent(ctx, n, n.Status, "accepted", 0, nil, 0, "")
	span.SetAttributes(attribute.String("notification.id", n.ID), attribute.String("notification.status", string(n.Status)))
	return n, nil
}

func (s *Service) CreateBatch(ctx context.Context, req domain.NotificationBatchRequest, correlationID string) (string, []domain.Notification, error) {
	ctx, span := tracer.Start(ctx, "service.create_batch",
		trace.WithAttributes(
			attribute.Int("notification.batch_size", len(req.Notifications)),
			attribute.String("tenant.id", httpx.TenantID(ctx)),
		),
	)
	defer span.End()
	if err := ctx.Err(); err != nil {
		recordSpanError(span, err)
		return "", nil, err
	}
	logger := httpx.Logger(ctx)
	tenantID := httpx.TenantID(ctx)
	if len(req.Notifications) == 0 {
		err := errors.New("notifications array is required")
		recordSpanError(span, err)
		return "", nil, err
	}
	if len(req.Notifications) > 1000 {
		err := errors.New("batch size cannot exceed 1000")
		recordSpanError(span, err)
		return "", nil, err
	}
	if req.IdempotencyKey != "" {
		if batchID, err := s.repo.GetBatchIDByKey(ctx, req.IdempotencyKey, tenantID); err == nil {
			existing, err := s.repo.GetByBatch(ctx, batchID, tenantID)
			return batchID, existing, err
		}
	}
	batchID := newID()
	now := time.Now().UTC()
	items := make([]domain.Notification, 0, len(req.Notifications))
	for _, item := range req.Notifications {
		if err := ctx.Err(); err != nil {
			recordSpanError(span, err)
			return "", nil, err
		}
		if err := validateRequest(item); err != nil {
			recordSpanError(span, err)
			return "", nil, err
		}
		status := domain.StatusPending
		if item.ScheduledAt != nil && item.ScheduledAt.After(now) {
			status = domain.StatusScheduled
		}
		items = append(items, domain.Notification{
			ID:             newID(),
			TenantID:       tenantID,
			BatchID:        batchID,
			Recipient:      item.Recipient,
			Channel:        item.Channel,
			Content:        item.Content,
			Priority:       normalizePriority(item.Priority),
			Status:         status,
			Attempts:       0,
			MaxAttempts:    3,
			ScheduledAt:    item.ScheduledAt,
			CreatedAt:      now,
			UpdatedAt:      now,
			CorrelationID:  correlationID,
			IdempotencyKey: item.IdempotencyKey,
		})
	}
	if err := s.repo.InsertNotifications(ctx, items); err != nil {
		recordSpanError(span, err)
		return "", nil, err
	}
	if req.IdempotencyKey != "" {
		if err := s.repo.UpsertRequestMapping(ctx, req.IdempotencyKey, batchID, tenantID); err != nil {
			recordSpanError(span, err)
			return "", nil, err
		}
	}
	if logger != nil {
		logger.Info("notification batch created", "batch_id", batchID, "count", len(items))
	}
	for _, item := range items {
		s.recordEvent(ctx, item, item.Status, "accepted", 0, nil, 0, "")
	}
	span.SetAttributes(attribute.String("notification.batch_id", batchID))
	return batchID, items, nil
}

func (s *Service) GetNotification(ctx context.Context, id string) (domain.Notification, error) {
	ctx, span := tracer.Start(ctx, "service.get_notification", trace.WithAttributes(attribute.String("notification.id", id), attribute.String("tenant.id", httpx.TenantID(ctx))))
	defer span.End()
	if err := ctx.Err(); err != nil {
		recordSpanError(span, err)
		return domain.Notification{}, err
	}
	n, err := s.repo.GetNotification(ctx, id, httpx.TenantID(ctx))
	if err != nil {
		recordSpanError(span, err)
	}
	return n, err
}

func (s *Service) GetBatch(ctx context.Context, batchID string) ([]domain.Notification, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.repo.GetByBatch(ctx, batchID, httpx.TenantID(ctx))
}

func (s *Service) List(ctx context.Context, status, channel string, from, to *time.Time, limit, offset int) ([]domain.Notification, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.repo.List(ctx, status, channel, from, to, limit, offset, httpx.TenantID(ctx))
}

func (s *Service) ListDeadLetters(ctx context.Context, channel string, limit, offset int) ([]domain.Notification, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.repo.ListDeadLetters(ctx, channel, limit, offset, httpx.TenantID(ctx))
}

func (s *Service) ListEvents(ctx context.Context, notificationID string, limit, offset int) ([]domain.DeliveryEvent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return s.repo.ListEvents(ctx, notificationID, limit, offset, httpx.TenantID(ctx))
}

func (s *Service) Cancel(ctx context.Context, ids []string) (int64, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	return s.repo.CancelPending(ctx, ids, httpx.TenantID(ctx))
}

func (s *Service) ProcessChannel(ctx context.Context, channel string) error {
	ctx, span := tracer.Start(ctx, "service.process_channel", trace.WithAttributes(attribute.String("notification.channel", channel)))
	defer span.End()
	if err := ctx.Err(); err != nil {
		recordSpanError(span, err)
		return err
	}
	logger := httpx.Logger(ctx)
	depth, err := s.repo.QueueDepthByChannel(ctx, channel)
	if err == nil {
		s.metrics.SetQueueDepth(channel, depth)
	}
	allowed, err := s.repo.AcquireRateLimitSlots(ctx, "delivery:"+channel, s.rateLimit, s.claimLimit)
	if err != nil {
		recordSpanError(span, err)
		return err
	}
	span.SetAttributes(attribute.Int("rate_limit.allowed", allowed), attribute.Int("worker.claim_limit", s.claimLimit))
	if allowed == 0 {
		return nil
	}
	claimed, err := s.repo.ClaimDue(ctx, channel, allowed)
	if err != nil {
		recordSpanError(span, err)
		return err
	}
	span.SetAttributes(attribute.Int("notification.claimed_count", len(claimed)))
	for _, notification := range claimed {
		notificationCtx, notificationSpan := tracer.Start(ctx, "service.deliver_notification",
			trace.WithAttributes(
				attribute.String("notification.id", notification.ID),
				attribute.String("notification.channel", string(notification.Channel)),
				attribute.String("notification.priority", string(notification.Priority)),
				attribute.Int("notification.attempt", notification.Attempts+1),
				attribute.String("tenant.id", notification.TenantID),
			),
		)
		if err := ctx.Err(); err != nil {
			recordSpanError(notificationSpan, err)
			notificationSpan.End()
			return err
		}
		start := time.Now()
		resp, statusCode, err := s.provider.Send(notificationCtx, provider.DeliveryRequest{
			To:      notification.Recipient,
			Channel: string(notification.Channel),
			Content: notification.Content,
		})
		attempt := notification.Attempts + 1
		notificationSpan.SetAttributes(attribute.Int("provider.status_code", statusCode))
		if err != nil || statusCode < 200 || statusCode >= 300 {
			if err != nil {
				recordSpanError(notificationSpan, err)
			}
			message := fmt.Sprintf("provider status=%d err=%v", statusCode, err)
			if shouldRetryDelivery(statusCode, err) && attempt < notification.MaxAttempts {
				nextAttemptAt := time.Now().Add(backoff(notification.Attempts))
				_ = s.repo.ScheduleRetry(ctx, notification.ID, message, nextAttemptAt)
				s.recordEvent(ctx, notification, domain.StatusScheduled, message, attempt, &nextAttemptAt, statusCode, "")
				if logger != nil {
					logger.Warn("notification delivery scheduled for retry", "notification_id", notification.ID, "channel", channel, "attempt", attempt)
				}
			} else if shouldRetryDelivery(statusCode, err) {
				_ = s.repo.MarkDeadLetter(ctx, notification.ID, message)
				s.recordEvent(ctx, notification, domain.StatusDeadLetter, message, attempt, nil, statusCode, "")
				if logger != nil {
					logger.Error("notification moved to dead letter", "notification_id", notification.ID, "channel", channel, "attempt", attempt)
				}
			} else {
				_ = s.repo.MarkFailed(ctx, notification.ID, message)
				s.recordEvent(ctx, notification, domain.StatusFailed, message, attempt, nil, statusCode, "")
				if logger != nil {
					logger.Error("notification delivery failed permanently", "notification_id", notification.ID, "channel", channel, "attempt", attempt)
				}
			}
			s.metrics.IncFailure(channel, time.Since(start))
			notificationSpan.SetStatus(codes.Error, message)
			notificationSpan.End()
			continue
		}
		_ = s.repo.MarkDelivered(ctx, notification.ID, resp.MessageID, time.Since(start))
		s.recordEvent(ctx, notification, domain.StatusDelivered, "provider accepted", attempt, nil, statusCode, resp.MessageID)
		if logger != nil {
			logger.Info("notification delivered", "notification_id", notification.ID, "channel", channel, "provider_message_id", resp.MessageID)
		}
		s.metrics.IncSuccess(channel, time.Since(start))
		notificationSpan.SetAttributes(attribute.String("provider.message_id", resp.MessageID), attribute.String("notification.status", string(domain.StatusDelivered)))
		notificationSpan.End()
	}
	return nil
}

func (s *Service) MetricsSnapshot(ctx context.Context) map[string]any {
	if err := ctx.Err(); err != nil {
		return map[string]any{"error": err.Error()}
	}
	for _, channel := range []string{"sms", "email", "push"} {
		if depth, err := s.repo.QueueDepthByChannel(ctx, channel); err == nil {
			s.metrics.SetQueueDepth(channel, depth)
		}
	}
	return s.metrics.Snapshot()
}

func validateRequest(req domain.NotificationRequest) error {
	if strings.TrimSpace(req.Recipient) == "" {
		return errors.New("recipient is required")
	}
	if strings.TrimSpace(req.Content) == "" {
		return errors.New("content is required")
	}
	switch req.Channel {
	case domain.ChannelSMS, domain.ChannelEmail, domain.ChannelPush:
	default:
		return errors.New("channel must be sms, email, or push")
	}
	limit := map[domain.Channel]int{domain.ChannelSMS: 1600, domain.ChannelEmail: 10000, domain.ChannelPush: 256}[req.Channel]
	if len(req.Content) > limit {
		return fmt.Errorf("content exceeds %d characters for %s", limit, req.Channel)
	}
	return nil
}

func normalizePriority(priority domain.Priority) domain.Priority {
	switch priority {
	case domain.PriorityHigh, domain.PriorityNormal, domain.PriorityLow:
		return priority
	default:
		return domain.PriorityNormal
	}
}

func newID() string {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("fallback-%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf)
}

func backoff(attempts int) time.Duration {
	base := 2 * time.Second
	if attempts <= 0 {
		return addJitter(base)
	}
	if attempts > 5 {
		attempts = 5
	}
	return addJitter(base * time.Duration(1<<attempts))
}

func addJitter(delay time.Duration) time.Duration {
	var buf [1]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return delay
	}
	jitter := time.Duration(buf[0]) * delay / 1024
	return delay + jitter
}

func shouldRetryDelivery(statusCode int, err error) bool {
	if err != nil {
		return true
	}
	if statusCode == http.StatusRequestTimeout || statusCode == http.StatusTooManyRequests {
		return true
	}
	return statusCode >= http.StatusInternalServerError
}

func (s *Service) recordEvent(ctx context.Context, n domain.Notification, status domain.Status, reason string, attempt int, nextAttemptAt *time.Time, providerStatus int, providerMessageID string) {
	_ = s.repo.RecordEvent(ctx, domain.DeliveryEvent{
		NotificationID:    n.ID,
		TenantID:          n.TenantID,
		BatchID:           n.BatchID,
		Channel:           n.Channel,
		Status:            status,
		Attempt:           attempt,
		Reason:            reason,
		NextAttemptAt:     nextAttemptAt,
		ProviderStatus:    providerStatus,
		ProviderMessageID: providerMessageID,
		CreatedAt:         time.Now().UTC(),
	})
}

func recordSpanError(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}
