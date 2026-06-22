package httpx

import (
	"context"
	"log/slog"
)

type contextKey string

const (
	requestIDKey   contextKey = "request_id"
	correlationKey contextKey = "correlation_id"
	loggerKey      contextKey = "logger"
	tenantKey      contextKey = "tenant_id"
)

func WithRequestID(ctx context.Context, requestID string) context.Context {
	return context.WithValue(ctx, requestIDKey, requestID)
}

func WithCorrelationID(ctx context.Context, correlationID string) context.Context {
	return context.WithValue(ctx, correlationKey, correlationID)
}

func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey, logger)
}

func WithTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, tenantKey, tenantID)
}

func RequestID(ctx context.Context) string {
	if value, ok := ctx.Value(requestIDKey).(string); ok {
		return value
	}
	return ""
}

func CorrelationID(ctx context.Context) string {
	if value, ok := ctx.Value(correlationKey).(string); ok {
		return value
	}
	return ""
}

func Logger(ctx context.Context) *slog.Logger {
	if value, ok := ctx.Value(loggerKey).(*slog.Logger); ok {
		return value
	}
	return slog.Default()
}

func TenantID(ctx context.Context) string {
	if value, ok := ctx.Value(tenantKey).(string); ok && value != "" {
		return value
	}
	return "default"
}
