package observability

import (
	"context"
	"sync"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type Metrics struct {
	mx            sync.RWMutex
	queueDepth    map[string]int
	successCount  map[string]int64
	failureCount  map[string]int64
	latencySum    map[string]time.Duration
	latencyCount  map[string]int64
	queueDepthCtr metric.Int64UpDownCounter
	successCtr    metric.Int64Counter
	failureCtr    metric.Int64Counter
	latencyHist   metric.Float64Histogram
}

func NewMetrics() *Metrics {
	meter := otel.Meter("insider-one")
	queueDepthCtr, _ := meter.Int64UpDownCounter("insider_one_queue_depth")
	successCtr, _ := meter.Int64Counter("insider_one_delivery_success_total")
	failureCtr, _ := meter.Int64Counter("insider_one_delivery_failure_total")
	latencyHist, _ := meter.Float64Histogram("insider_one_delivery_latency_ms")

	return &Metrics{
		queueDepth:    map[string]int{},
		successCount:  map[string]int64{},
		failureCount:  map[string]int64{},
		latencySum:    map[string]time.Duration{},
		latencyCount:  map[string]int64{},
		queueDepthCtr: queueDepthCtr,
		successCtr:    successCtr,
		failureCtr:    failureCtr,
		latencyHist:   latencyHist,
	}
}

func (m *Metrics) SetQueueDepth(channel string, depth int) {
	m.mx.Lock()
	defer m.mx.Unlock()
	previous := m.queueDepth[channel]
	delta := depth - previous
	m.queueDepth[channel] = depth
	if m.queueDepthCtr != nil && delta != 0 {
		m.queueDepthCtr.Add(context.Background(), int64(delta), metric.WithAttributes(attribute.String("channel", channel)))
	}
}

func (m *Metrics) IncSuccess(channel string, latency time.Duration) {
	m.mx.Lock()
	defer m.mx.Unlock()
	m.successCount[channel]++
	m.latencySum[channel] += latency
	m.latencyCount[channel]++
	if m.successCtr != nil {
		m.successCtr.Add(context.Background(), 1, metric.WithAttributes(attribute.String("channel", channel)))
	}
	if m.latencyHist != nil {
		m.latencyHist.Record(context.Background(), float64(latency.Milliseconds()), metric.WithAttributes(attribute.String("channel", channel), attribute.String("status", "success")))
	}
}

func (m *Metrics) IncFailure(channel string, latency time.Duration) {
	m.mx.Lock()
	defer m.mx.Unlock()
	m.failureCount[channel]++
	m.latencySum[channel] += latency
	m.latencyCount[channel]++
	if m.failureCtr != nil {
		m.failureCtr.Add(context.Background(), 1, metric.WithAttributes(attribute.String("channel", channel)))
	}
	if m.latencyHist != nil {
		m.latencyHist.Record(context.Background(), float64(latency.Milliseconds()), metric.WithAttributes(attribute.String("channel", channel), attribute.String("status", "failure")))
	}
}

func (m *Metrics) Snapshot() map[string]any {
	m.mx.RLock()
	defer m.mx.RUnlock()
	channels := map[string]any{}
	for _, channel := range []string{"sms", "email", "push"} {
		count := m.latencyCount[channel]
		avg := time.Duration(0)
		if count > 0 {
			avg = m.latencySum[channel] / time.Duration(count)
		}
		channels[channel] = map[string]any{
			"queueDepth":       m.queueDepth[channel],
			"successCount":     m.successCount[channel],
			"failureCount":     m.failureCount[channel],
			"averageLatencyMs": avg.Milliseconds(),
		}
	}
	return map[string]any{"channels": channels}
}
