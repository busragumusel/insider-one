# Insider One Notification System

Scalable event-driven notification service in Go with asynchronous processing, per-channel rate limiting, persistence, and delivery status tracking.

## Architecture

The code is intentionally split into a few small packages so the control flow is easy to follow:

- `internal/app` wires HTTP routes, middleware, and the worker lifecycle.
- `internal/service` owns validation, idempotency, retry policy, and delivery decisions.
- `internal/repository` is the persistence boundary and keeps the SQL isolated.
- `internal/worker` drives the channel loops and enforces the fixed cadence per channel.
- `internal/httpx` carries request metadata and the thin logging middleware.

That layout keeps the API layer thin and makes the worker behavior explicit instead of burying it inside the handler code.

## Run

```bash
docker compose up --build
```

By default the app uses `noop://accepted`, an in-process provider adapter that accepts deliveries without requiring webhook.site. Set `PROVIDER_URL` to a webhook.site URL when you want to exercise the external provider integration.

Prometheus is available at `http://localhost:9090` and scrapes the OpenTelemetry collector, which receives metrics from the app over OTLP.

Jaeger is available at `http://localhost:16686` and receives distributed traces from the same OpenTelemetry collector.

Logs are emitted as JSON with request and correlation metadata so they can be shipped directly to Loki, Elasticsearch, or any other structured log sink.

Swagger UI is available at `http://localhost:8080/swagger`, and the generated OpenAPI document is served at `http://localhost:8080/openapi.yaml`. The OpenAPI spec includes request bodies, response envelopes, reusable errors, auth headers, tenant headers, and query/path parameters so Swagger can be used as an API console.

or locally:

```bash
CGO_ENABLED=0 go test ./...
go run ./cmd/insider-one
```

Run the full integration suite against PostgreSQL:

```bash
CGO_ENABLED=0 TEST_DATABASE_URL=postgres://insider:insider@localhost:5432/insider_one?sslmode=disable go test ./...
```

If `TEST_DATABASE_URL` is not set, integration tests try to start a disposable PostgreSQL container with Testcontainers. If Docker is not running, those tests skip with a clear message.

For a throughput check, run:

```bash
CGO_ENABLED=0 TEST_DATABASE_URL=postgres://insider:insider@localhost:5432/insider_one?sslmode=disable go test ./internal/service -bench=BenchmarkCreateBatchHighThroughput -benchmem
```

## Environment

- `DATABASE_URL`: PostgreSQL connection URL, default `postgres://insider:insider@localhost:5432/insider_one?sslmode=disable`
- `PROVIDER_URL`: provider endpoint, default `noop://accepted` for demos without an external provider. Set this to a webhook.site URL to exercise the assessment provider integration.
- `SMS_PROVIDER_URL`, `EMAIL_PROVIDER_URL`, `PUSH_PROVIDER_URL`: optional channel-specific provider URLs. When omitted, `PROVIDER_URL` is used for every channel.
- `LISTEN_ADDR`: HTTP listen address, default `:8080`
- `OTEL_ENABLED`: enable OpenTelemetry export, default `true`
- `OTEL_EXPORTER_OTLP_ENDPOINT`: OTLP gRPC endpoint, default `otel-collector:4317`
- `OTEL_SERVICE_NAME`: service name reported to telemetry backends, default `insider-one-notifications`
- `WORKER_CONCURRENCY`: number of workers per channel, default `4`
- `WORKER_CLAIM_LIMIT`: notifications claimed by each worker cycle, default `25`
- `RATE_LIMIT_PER_SECOND`: shared per-channel delivery slots per second, default `100`
- `API_KEYS`: comma-separated API keys. Leave empty to disable auth for local/demo use.

When `API_KEYS` is set, protected endpoints require `X-API-Key`. Requests may also include `X-Tenant-Id`; when omitted, the tenant defaults to `default`.

## API Examples

Create one notification:

```bash
curl -X POST http://localhost:8080/notifications \
  -H 'Content-Type: application/json' \
  -H 'X-Tenant-Id: demo' \
  -H 'Idempotency-Key: notif-demo-1' \
  -d '{
    "recipient": "+905551234567",
    "channel": "sms",
    "content": "Flash sale starts now",
    "priority": "high"
  }'
```

Create a batch:

```bash
curl -X POST http://localhost:8080/notifications/batches \
  -H 'Content-Type: application/json' \
  -H 'X-Tenant-Id: demo' \
  -d '{
    "idempotencyKey": "campaign-2026-06-22",
    "notifications": [
      {
        "recipient": "+905551234567",
        "channel": "sms",
        "content": "Flash sale starts now",
        "priority": "high"
      },
      {
        "recipient": "user@example.com",
        "channel": "email",
        "content": "Your weekly digest is ready",
        "priority": "normal"
      }
    ]
  }'
```

Check status and delivery history:

```bash
curl http://localhost:8080/notifications/{id} -H 'X-Tenant-Id: demo'
curl http://localhost:8080/notifications/{id}/events -H 'X-Tenant-Id: demo'
```

## API

- `POST /notifications`
- `POST /notifications/batches`
- `GET /notifications/{id}`
- `GET /notifications/{id}/events`
- `GET /notifications/dead-letter?channel=&limit=&offset=`
- `GET /batches/{id}`
- `PATCH /notifications/{id}/cancel`
- `POST /notifications/cancel`
- `GET /notifications?status=&channel=&from=&to=&limit=&offset=`
- `GET /metrics`
- `GET /health`

Responses are wrapped in explicit envelopes that include request metadata. The list endpoint also returns pagination details so clients can page deterministically without guessing the payload shape.

## Distributed Tracing

The service emits OpenTelemetry traces over OTLP. Traces include:

- inbound HTTP spans with request IDs, correlation IDs, status codes, and trace IDs in logs
- service spans for create, batch, query, cancellation, worker processing, and per-notification delivery
- repository spans for PostgreSQL inserts, claims, status transitions, event writes, list queries, and rate-limit slot acquisition
- outbound provider spans that propagate W3C `traceparent`/`baggage` headers to webhook.site

With Docker Compose, traces flow from the app to the OpenTelemetry collector and then to Jaeger. Open `http://localhost:16686`, select `insider-one-notifications`, and search recent traces.

## CI

GitHub Actions is configured in `.github/workflows/ci.yml`. It starts PostgreSQL and runs:

```bash
CGO_ENABLED=0 go test ./... -count=1
```

## Production Readiness

The current implementation is suitable for a stronger demo or small internal workload, but it is not yet a complete production system for millions of notifications per day. The main constraints are:

- PostgreSQL is used as the persistence layer with row-lock based claiming, but the database is still doing queue-like work directly.
- Workers are configurable and batched, but still run in-process with polling, so delivery throughput is not horizontally distributed.
- Rate limiting is shared through PostgreSQL windows, which is good for this take-home scope but should move to Redis or a dedicated gateway at larger scale.
- Retry scheduling is implemented with polling and jittered backoff, not with a durable delayed-job queue.
- Backpressure and queue partitioning are not yet in place.

To make this production-ready for burst traffic and millions of daily notifications, the next upgrade path is:

1. Replace the in-process polling loop with a real queue or job system.
2. Add shared per-channel rate limiting across all workers.
3. Move delayed retries to a durable delayed-job mechanism.
4. Scale workers horizontally and partition work by channel or tenant.
5. Keep the current observability stack so internal teams and API consumers still have logs, metrics, traces, health, and Swagger.

### Queue Strategy

This implementation intentionally uses PostgreSQL as the durable queue with `FOR UPDATE SKIP LOCKED`. That keeps the assessment self-contained and makes `docker compose up` enough to run the system. For a larger production deployment, the same service boundary should publish jobs to Kafka, SQS, NATS, RabbitMQ, or another dedicated queue while PostgreSQL remains the source of truth for notification state and delivery history.

## Notes

- Batch size is limited to 1000 notifications.
- Per-channel throughput is controlled by worker concurrency and claim size.
- Per-channel delivery rate is enforced through a PostgreSQL-backed shared rate-limit window.
- Idempotency is supported with the `Idempotency-Key` header.
- Retries use exponential backoff with jitter, non-retryable provider responses fail immediately, and exhausted retryable responses move to `dead_letter`.
- Delivery events are persisted so clients can inspect accepted, delivered, failed, retry, and dead-letter transitions.
- Requests emit `X-Request-Id` and `X-Correlation-Id` headers for traceability.
