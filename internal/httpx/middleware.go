package httpx

import (
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("insider-one/http")

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func WithAPIKeyAuth(apiKeys []string, next http.Handler) http.Handler {
	allowed := map[string]struct{}{}
	for _, key := range apiKeys {
		if key != "" {
			allowed[key] = struct{}{}
		}
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tenantID := r.Header.Get("X-Tenant-Id")
		if tenantID == "" {
			tenantID = "default"
		}
		if len(allowed) == 0 || isPublicRoute(r) {
			next.ServeHTTP(w, r.WithContext(WithTenantID(r.Context(), tenantID)))
			return
		}
		apiKey := r.Header.Get("X-API-Key")
		if !containsKey(allowed, apiKey) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing or invalid API key", "code": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r.WithContext(WithTenantID(r.Context(), tenantID)))
	})
}

func WithRequestMetadata(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))
		ctx, span := tracer.Start(ctx, r.Method+" "+r.URL.Path,
			trace.WithSpanKind(trace.SpanKindServer),
			trace.WithAttributes(
				attribute.String("http.request.method", r.Method),
				attribute.String("url.path", r.URL.Path),
				attribute.String("tenant.id", TenantID(r.Context())),
			),
		)
		defer span.End()

		requestID := r.Header.Get("X-Request-Id")
		if requestID == "" {
			requestID = requestIDFromClock()
		}
		correlationID := r.Header.Get("X-Correlation-Id")
		if correlationID == "" {
			correlationID = requestID
		}

		w.Header().Set("X-Request-Id", requestID)
		w.Header().Set("X-Correlation-Id", correlationID)

		if logger == nil {
			logger = slog.Default()
		}
		requestLogger := logger.With(
			"request_id", requestID,
			"correlation_id", correlationID,
			"method", r.Method,
			"path", r.URL.Path,
			"trace_id", span.SpanContext().TraceID().String(),
		)
		ctx = WithLogger(WithCorrelationID(WithRequestID(ctx, requestID), correlationID), requestLogger)
		startedAt := time.Now()
		if requestLogger != nil {
			requestLogger.Info("http request started")
		}

		recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(recorder, r.WithContext(ctx))
		span.SetAttributes(attribute.Int("http.response.status_code", recorder.status))
		if recorder.status >= http.StatusInternalServerError {
			span.SetStatus(codes.Error, http.StatusText(recorder.status))
		}

		if requestLogger != nil {
			requestLogger.Info("http request finished", "duration_ms", time.Since(startedAt).Milliseconds(), "status", recorder.status)
		}
	})
}

func requestIDFromClock() string {
	return time.Now().UTC().Format("20060102T150405.000000000Z07:00")
}

func isPublicRoute(r *http.Request) bool {
	if r.Method == http.MethodGet {
		switch r.URL.Path {
		case "/health", "/swagger", "/openapi.yaml":
			return true
		}
	}
	return false
}

func containsKey(allowed map[string]struct{}, provided string) bool {
	if provided == "" {
		return false
	}
	for key := range allowed {
		if subtle.ConstantTimeCompare([]byte(key), []byte(provided)) == 1 {
			return true
		}
	}
	return false
}
