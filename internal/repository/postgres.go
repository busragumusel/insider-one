package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"insider-one/internal/domain"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("insider-one/repository")

type Repository struct {
	db *sql.DB
}

func New(databaseURL string) (*Repository, error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(25)
	db.SetConnMaxLifetime(30 * time.Minute)
	if _, err := db.ExecContext(context.Background(), schema); err != nil {
		_ = db.Close()
		return nil, err
	}
	return &Repository{db: db}, nil
}

func (r *Repository) Close() error { return r.db.Close() }

const schema = `
CREATE TABLE IF NOT EXISTS notification_requests (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL DEFAULT 'default',
  idempotency_key TEXT UNIQUE,
  batch_id TEXT NOT NULL,
  created_at TIMESTAMPTZ NOT NULL
);
CREATE TABLE IF NOT EXISTS notifications (
  id TEXT PRIMARY KEY,
  tenant_id TEXT NOT NULL DEFAULT 'default',
  batch_id TEXT NOT NULL,
  recipient TEXT NOT NULL,
  channel TEXT NOT NULL,
  content TEXT NOT NULL,
  priority TEXT NOT NULL,
  status TEXT NOT NULL,
  attempts INTEGER NOT NULL DEFAULT 0,
  max_attempts INTEGER NOT NULL DEFAULT 3,
  scheduled_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL,
  updated_at TIMESTAMPTZ NOT NULL,
  correlation_id TEXT,
  idempotency_key TEXT UNIQUE,
  provider_message_id TEXT,
  last_error TEXT
);
ALTER TABLE notification_requests ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE notification_requests DROP CONSTRAINT IF EXISTS notification_requests_idempotency_key_key;
CREATE UNIQUE INDEX IF NOT EXISTS idx_notification_requests_tenant_idempotency ON notification_requests(tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
ALTER TABLE notifications ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT 'default';
ALTER TABLE notifications DROP CONSTRAINT IF EXISTS notifications_idempotency_key_key;
DROP INDEX IF EXISTS idx_notifications_idempotency;
CREATE INDEX IF NOT EXISTS idx_notifications_status_channel_created ON notifications(status, channel, created_at);
CREATE INDEX IF NOT EXISTS idx_notifications_due ON notifications(channel, status, scheduled_at, priority, created_at);
CREATE INDEX IF NOT EXISTS idx_notifications_tenant_batch ON notifications(tenant_id, batch_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_notifications_tenant_idempotency ON notifications(tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL;
CREATE TABLE IF NOT EXISTS notification_events (
  id BIGSERIAL PRIMARY KEY,
  tenant_id TEXT NOT NULL DEFAULT 'default',
  notification_id TEXT NOT NULL REFERENCES notifications(id) ON DELETE CASCADE,
  batch_id TEXT NOT NULL,
  channel TEXT NOT NULL,
  status TEXT NOT NULL,
  attempt INTEGER NOT NULL DEFAULT 0,
  reason TEXT,
  next_attempt_at TIMESTAMPTZ,
  provider_status INTEGER NOT NULL DEFAULT 0,
  provider_message_id TEXT,
  created_at TIMESTAMPTZ NOT NULL
);
ALTER TABLE notification_events ADD COLUMN IF NOT EXISTS tenant_id TEXT NOT NULL DEFAULT 'default';
CREATE INDEX IF NOT EXISTS idx_notification_events_notification ON notification_events(notification_id, id);
CREATE INDEX IF NOT EXISTS idx_notification_events_status_created ON notification_events(status, created_at);
CREATE INDEX IF NOT EXISTS idx_notification_events_tenant_notification ON notification_events(tenant_id, notification_id, id);
CREATE TABLE IF NOT EXISTS rate_limit_windows (
  key TEXT NOT NULL,
  window_start TIMESTAMPTZ NOT NULL,
  limit_count INTEGER NOT NULL,
  used_count INTEGER NOT NULL DEFAULT 0,
  PRIMARY KEY (key, window_start)
);
`

func (r *Repository) InsertNotifications(ctx context.Context, notifications []domain.Notification) error {
	ctx, span := tracer.Start(ctx, "repository.insert_notifications", trace.WithAttributes(attribute.Int("notification.count", len(notifications))))
	defer span.End()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		recordSpanError(span, err)
		return err
	}
	defer func() { _ = tx.Rollback() }()
	for _, n := range notifications {
		var scheduledAt any
		if n.ScheduledAt != nil {
			scheduledAt = n.ScheduledAt.UTC()
		}
		_, err := tx.ExecContext(ctx, `INSERT INTO notifications (
 id, tenant_id, batch_id, recipient, channel, content, priority, status, attempts, max_attempts, scheduled_at, created_at, updated_at, correlation_id, idempotency_key, provider_message_id, last_error
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17)`,
			n.ID, tenantID(n.TenantID), n.BatchID, n.Recipient, string(n.Channel), n.Content, string(n.Priority), string(n.Status), n.Attempts, n.MaxAttempts, scheduledAt, n.CreatedAt.UTC(), n.UpdatedAt.UTC(), nullString(n.CorrelationID), nullString(n.IdempotencyKey), nullString(n.ProviderMessageID), nullString(n.LastError))
		if err != nil {
			recordSpanError(span, err)
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		recordSpanError(span, err)
		return err
	}
	return nil
}

func (r *Repository) RecordEvent(ctx context.Context, event domain.DeliveryEvent) error {
	ctx, span := tracer.Start(ctx, "repository.record_event",
		trace.WithAttributes(
			attribute.String("notification.id", event.NotificationID),
			attribute.String("notification.status", string(event.Status)),
			attribute.String("tenant.id", tenantID(event.TenantID)),
		),
	)
	defer span.End()
	var nextAttemptAt any
	if event.NextAttemptAt != nil {
		nextAttemptAt = event.NextAttemptAt.UTC()
	}
	createdAt := event.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `INSERT INTO notification_events (
 tenant_id, notification_id, batch_id, channel, status, attempt, reason, next_attempt_at, provider_status, provider_message_id, created_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`,
		tenantID(event.TenantID), event.NotificationID, event.BatchID, string(event.Channel), string(event.Status), event.Attempt, nullString(event.Reason), nextAttemptAt, event.ProviderStatus, nullString(event.ProviderMessageID), createdAt.UTC())
	if err != nil {
		recordSpanError(span, err)
	}
	return err
}

func (r *Repository) UpsertRequestMapping(ctx context.Context, key string, batchID string, tenant string) error {
	_, err := r.db.ExecContext(ctx, `INSERT INTO notification_requests (tenant_id, idempotency_key, batch_id, created_at) VALUES ($1, $2, $3, $4) ON CONFLICT(tenant_id, idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING`, tenantID(tenant), key, batchID, time.Now().UTC())
	return err
}

func (r *Repository) GetBatchIDByKey(ctx context.Context, key string, tenant string) (string, error) {
	var batchID string
	err := r.db.QueryRowContext(ctx, `SELECT batch_id FROM notification_requests WHERE idempotency_key = $1 AND tenant_id = $2`, key, tenantID(tenant)).Scan(&batchID)
	if err != nil {
		return "", err
	}
	return batchID, nil
}

func (r *Repository) GetNotification(ctx context.Context, id, tenant string) (domain.Notification, error) {
	ctx, span := tracer.Start(ctx, "repository.get_notification", trace.WithAttributes(attribute.String("notification.id", id), attribute.String("tenant.id", tenantID(tenant))))
	defer span.End()
	row := r.db.QueryRowContext(ctx, notificationSelectSQL(`WHERE id = $1 AND tenant_id = $2`), id, tenantID(tenant))
	n, err := scanNotification(row)
	if err != nil {
		recordSpanError(span, err)
	}
	return n, err
}

func (r *Repository) GetByIdempotencyKey(ctx context.Context, key, tenant string) (domain.Notification, error) {
	row := r.db.QueryRowContext(ctx, notificationSelectSQL(`WHERE idempotency_key = $1 AND tenant_id = $2`), key, tenantID(tenant))
	return scanNotification(row)
}

func (r *Repository) GetByBatch(ctx context.Context, batchID, tenant string) ([]domain.Notification, error) {
	rows, err := r.db.QueryContext(ctx, notificationSelectSQL(`WHERE batch_id = $1 AND tenant_id = $2 ORDER BY created_at ASC`), batchID, tenantID(tenant))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanNotifications(rows)
}

func (r *Repository) List(ctx context.Context, status, channel string, from, to *time.Time, limit, offset int, tenant string) ([]domain.Notification, error) {
	ctx, span := tracer.Start(ctx, "repository.list_notifications",
		trace.WithAttributes(
			attribute.String("notification.status", status),
			attribute.String("notification.channel", channel),
			attribute.String("tenant.id", tenantID(tenant)),
			attribute.Int("pagination.limit", limit),
			attribute.Int("pagination.offset", offset),
		),
	)
	defer span.End()
	clauses := []string{"tenant_id = $1"}
	args := []any{tenantID(tenant)}
	if status != "" {
		args = append(args, status)
		clauses = append(clauses, fmt.Sprintf("status = $%d", len(args)))
	}
	if channel != "" {
		args = append(args, channel)
		clauses = append(clauses, fmt.Sprintf("channel = $%d", len(args)))
	}
	if from != nil {
		args = append(args, from.UTC())
		clauses = append(clauses, fmt.Sprintf("created_at >= $%d", len(args)))
	}
	if to != nil {
		args = append(args, to.UTC())
		clauses = append(clauses, fmt.Sprintf("created_at <= $%d", len(args)))
	}
	args = append(args, limit, offset)
	query := notificationSelectSQL("WHERE " + strings.Join(clauses, " AND ") + fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d OFFSET $%d", len(args)-1, len(args)))
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		recordSpanError(span, err)
		return nil, err
	}
	defer rows.Close()
	items, err := scanNotifications(rows)
	if err != nil {
		recordSpanError(span, err)
	}
	span.SetAttributes(attribute.Int("notification.returned_count", len(items)))
	return items, err
}

func (r *Repository) ListDeadLetters(ctx context.Context, channel string, limit, offset int, tenant string) ([]domain.Notification, error) {
	return r.List(ctx, string(domain.StatusDeadLetter), channel, nil, nil, limit, offset, tenant)
}

func (r *Repository) ListEvents(ctx context.Context, notificationID string, limit, offset int, tenant string) ([]domain.DeliveryEvent, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id, tenant_id, notification_id, batch_id, channel, status, attempt, reason, next_attempt_at, provider_status, provider_message_id, created_at FROM notification_events WHERE notification_id = $1 AND tenant_id = $2 ORDER BY id ASC LIMIT $3 OFFSET $4`, notificationID, tenantID(tenant), limit, offset)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var events []domain.DeliveryEvent
	for rows.Next() {
		event, err := scanEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func (r *Repository) CancelPending(ctx context.Context, ids []string, tenant string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	args := []any{string(domain.StatusCancelled), time.Now().UTC(), tenantID(tenant)}
	for _, id := range ids {
		args = append(args, id)
	}
	args = append(args, string(domain.StatusPending), string(domain.StatusScheduled), string(domain.StatusProcessing))
	query := fmt.Sprintf(`UPDATE notifications SET status = $1, updated_at = $2 WHERE tenant_id = $3 AND id IN (%s) AND status IN ($%d, $%d, $%d)`,
		placeholders(4, len(ids)), len(args)-2, len(args)-1, len(args))
	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func (r *Repository) ClaimDue(ctx context.Context, channel string, limit int) ([]domain.Notification, error) {
	ctx, span := tracer.Start(ctx, "repository.claim_due", trace.WithAttributes(attribute.String("notification.channel", channel), attribute.Int("worker.claim_limit", limit)))
	defer span.End()
	rows, err := r.db.QueryContext(ctx, `
WITH candidate AS (
  SELECT id
  FROM notifications
  WHERE channel = $1
    AND status IN ($2, $3)
    AND (scheduled_at IS NULL OR scheduled_at <= $4)
  ORDER BY CASE priority WHEN 'high' THEN 0 WHEN 'normal' THEN 1 ELSE 2 END, created_at ASC
  LIMIT $5
  FOR UPDATE SKIP LOCKED
)
UPDATE notifications AS n
SET status = $6, updated_at = $7
FROM candidate
WHERE n.id = candidate.id
RETURNING n.id, n.tenant_id, n.batch_id, n.recipient, n.channel, n.content, n.priority, n.status, n.attempts, n.max_attempts, n.scheduled_at, n.created_at, n.updated_at, n.correlation_id, n.idempotency_key, n.provider_message_id, n.last_error`,
		channel, string(domain.StatusPending), string(domain.StatusScheduled), time.Now().UTC(), limit, string(domain.StatusProcessing), time.Now().UTC())
	if err != nil {
		recordSpanError(span, err)
		return nil, err
	}
	defer rows.Close()
	items, err := scanNotifications(rows)
	if err != nil {
		recordSpanError(span, err)
	}
	span.SetAttributes(attribute.Int("notification.claimed_count", len(items)))
	return items, err
}

func (r *Repository) MarkDelivered(ctx context.Context, id, providerMessageID string, latency time.Duration) error {
	ctx, span := tracer.Start(ctx, "repository.mark_delivered", trace.WithAttributes(attribute.String("notification.id", id), attribute.String("provider.message_id", providerMessageID)))
	defer span.End()
	_, err := r.db.ExecContext(ctx, `UPDATE notifications SET status = $1, provider_message_id = $2, attempts = attempts + 1, updated_at = $3 WHERE id = $4`, string(domain.StatusDelivered), providerMessageID, time.Now().UTC(), id)
	if err != nil {
		recordSpanError(span, err)
	}
	return err
}

func (r *Repository) MarkFailed(ctx context.Context, id, message string) error {
	ctx, span := tracer.Start(ctx, "repository.mark_failed", trace.WithAttributes(attribute.String("notification.id", id)))
	defer span.End()
	_, err := r.db.ExecContext(ctx, `UPDATE notifications SET status = $1, attempts = attempts + 1, last_error = $2, scheduled_at = NULL, updated_at = $3 WHERE id = $4`, string(domain.StatusFailed), message, time.Now().UTC(), id)
	if err != nil {
		recordSpanError(span, err)
	}
	return err
}

func (r *Repository) MarkDeadLetter(ctx context.Context, id, message string) error {
	ctx, span := tracer.Start(ctx, "repository.mark_dead_letter", trace.WithAttributes(attribute.String("notification.id", id)))
	defer span.End()
	_, err := r.db.ExecContext(ctx, `UPDATE notifications SET status = $1, attempts = attempts + 1, last_error = $2, scheduled_at = NULL, updated_at = $3 WHERE id = $4`, string(domain.StatusDeadLetter), message, time.Now().UTC(), id)
	if err != nil {
		recordSpanError(span, err)
	}
	return err
}

func (r *Repository) ScheduleRetry(ctx context.Context, id, message string, nextAttemptAt time.Time) error {
	ctx, span := tracer.Start(ctx, "repository.schedule_retry", trace.WithAttributes(attribute.String("notification.id", id), attribute.String("notification.next_attempt_at", nextAttemptAt.Format(time.RFC3339Nano))))
	defer span.End()
	_, err := r.db.ExecContext(ctx, `UPDATE notifications SET status = $1, attempts = attempts + 1, last_error = $2, scheduled_at = $3, updated_at = $4 WHERE id = $5`, string(domain.StatusScheduled), message, nextAttemptAt.UTC(), time.Now().UTC(), id)
	if err != nil {
		recordSpanError(span, err)
	}
	return err
}

func (r *Repository) QueueDepthByChannel(ctx context.Context, channel string) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM notifications WHERE channel = $1 AND status IN ($2, $3)`, channel, string(domain.StatusPending), string(domain.StatusScheduled)).Scan(&count)
	return count, err
}

func (r *Repository) AcquireRateLimitSlots(ctx context.Context, key string, limit, requested int) (int, error) {
	ctx, span := tracer.Start(ctx, "repository.acquire_rate_limit_slots",
		trace.WithAttributes(attribute.String("rate_limit.key", key), attribute.Int("rate_limit.limit", limit), attribute.Int("rate_limit.requested", requested)),
	)
	defer span.End()
	if limit <= 0 || requested <= 0 {
		return 0, nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		recordSpanError(span, err)
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()

	windowStart := time.Now().UTC().Truncate(time.Second)
	if _, err := tx.ExecContext(ctx, `INSERT INTO rate_limit_windows (key, window_start, limit_count, used_count) VALUES ($1, $2, $3, 0) ON CONFLICT (key, window_start) DO UPDATE SET limit_count = EXCLUDED.limit_count`, key, windowStart, limit); err != nil {
		recordSpanError(span, err)
		return 0, err
	}
	var used int
	if err := tx.QueryRowContext(ctx, `SELECT used_count FROM rate_limit_windows WHERE key = $1 AND window_start = $2 FOR UPDATE`, key, windowStart).Scan(&used); err != nil {
		recordSpanError(span, err)
		return 0, err
	}
	remaining := limit - used
	if remaining <= 0 {
		return 0, tx.Commit()
	}
	acquired := requested
	if acquired > remaining {
		acquired = remaining
	}
	if _, err := tx.ExecContext(ctx, `UPDATE rate_limit_windows SET used_count = used_count + $1 WHERE key = $2 AND window_start = $3`, acquired, key, windowStart); err != nil {
		recordSpanError(span, err)
		return 0, err
	}
	span.SetAttributes(attribute.Int("rate_limit.acquired", acquired), attribute.Int("rate_limit.used_before", used))
	if err := tx.Commit(); err != nil {
		recordSpanError(span, err)
		return 0, err
	}
	return acquired, nil
}

func notificationSelectSQL(suffix string) string {
	return `SELECT id, tenant_id, batch_id, recipient, channel, content, priority, status, attempts, max_attempts, scheduled_at, created_at, updated_at, correlation_id, idempotency_key, provider_message_id, last_error FROM notifications ` + suffix
}

func scanNotifications(rows *sql.Rows) ([]domain.Notification, error) {
	var items []domain.Notification
	for rows.Next() {
		notification, err := scanNotification(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, notification)
	}
	return items, rows.Err()
}

func scanNotification(scanner interface{ Scan(dest ...any) error }) (domain.Notification, error) {
	var n domain.Notification
	var channel, priority, status string
	var scheduledAt, createdAt, updatedAt sql.NullTime
	var correlationID, idempotencyKey, providerMessageID, lastError sql.NullString
	if err := scanner.Scan(&n.ID, &n.TenantID, &n.BatchID, &n.Recipient, &channel, &n.Content, &priority, &status, &n.Attempts, &n.MaxAttempts, &scheduledAt, &createdAt, &updatedAt, &correlationID, &idempotencyKey, &providerMessageID, &lastError); err != nil {
		return domain.Notification{}, err
	}
	n.Channel = domain.Channel(channel)
	n.Priority = domain.Priority(priority)
	n.Status = domain.Status(status)
	if scheduledAt.Valid {
		t := scheduledAt.Time.UTC()
		n.ScheduledAt = &t
	}
	if createdAt.Valid {
		n.CreatedAt = createdAt.Time.UTC()
	}
	if updatedAt.Valid {
		n.UpdatedAt = updatedAt.Time.UTC()
	}
	if correlationID.Valid {
		n.CorrelationID = correlationID.String
	}
	if idempotencyKey.Valid {
		n.IdempotencyKey = idempotencyKey.String
	}
	if providerMessageID.Valid {
		n.ProviderMessageID = providerMessageID.String
	}
	if lastError.Valid {
		n.LastError = lastError.String
	}
	return n, nil
}

func scanEvent(scanner interface{ Scan(dest ...any) error }) (domain.DeliveryEvent, error) {
	var event domain.DeliveryEvent
	var channel, status string
	var reason, providerMessageID sql.NullString
	var nextAttemptAt, createdAt sql.NullTime
	if err := scanner.Scan(&event.ID, &event.TenantID, &event.NotificationID, &event.BatchID, &channel, &status, &event.Attempt, &reason, &nextAttemptAt, &event.ProviderStatus, &providerMessageID, &createdAt); err != nil {
		return domain.DeliveryEvent{}, err
	}
	event.Channel = domain.Channel(channel)
	event.Status = domain.Status(status)
	if reason.Valid {
		event.Reason = reason.String
	}
	if nextAttemptAt.Valid {
		t := nextAttemptAt.Time.UTC()
		event.NextAttemptAt = &t
	}
	if providerMessageID.Valid {
		event.ProviderMessageID = providerMessageID.String
	}
	if createdAt.Valid {
		event.CreatedAt = createdAt.Time.UTC()
	}
	return event, nil
}

func placeholders(start, count int) string {
	parts := make([]string, count)
	for i := 0; i < count; i++ {
		parts[i] = fmt.Sprintf("$%d", start+i)
	}
	return strings.Join(parts, ",")
}

func nullString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func tenantID(value string) string {
	if strings.TrimSpace(value) == "" {
		return "default"
	}
	return value
}

func recordSpanError(span trace.Span, err error) {
	if err == nil {
		return
	}
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
}

var ErrNotFound = errors.New("not found")

func isUniqueConstraintError(err error) bool {
	if err == nil {
		return false
	}
	lower := strings.ToLower(err.Error())
	return strings.Contains(lower, "duplicate key value violates unique constraint") || strings.Contains(lower, "unique constraint")
}

func wrapUniqueConstraint(err error, key string) error {
	if isUniqueConstraintError(err) {
		return fmt.Errorf("idempotency key %s already exists", key)
	}
	return err
}
