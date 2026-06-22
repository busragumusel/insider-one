package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/trace"
)

var tracer = otel.Tracer("insider-one/provider")

type DeliveryRequest struct {
	To      string `json:"to"`
	Channel string `json:"channel"`
	Content string `json:"content"`
}

type DeliveryResponse struct {
	MessageID string    `json:"messageId"`
	Status    string    `json:"status"`
	Timestamp time.Time `json:"timestamp"`
}

type Sender interface {
	Send(ctx context.Context, req DeliveryRequest) (DeliveryResponse, int, error)
}

type Client struct {
	senders map[string]Sender
}

type WebhookSender struct {
	channel    string
	endpoint   string
	httpClient *http.Client
}

func New(endpoint string) *Client {
	return NewWithEndpoints(map[string]string{
		"default": endpoint,
		"sms":     endpoint,
		"email":   endpoint,
		"push":    endpoint,
	})
}

func NewWithEndpoints(endpoints map[string]string) *Client {
	senders := map[string]Sender{}
	for _, channel := range []string{"sms", "email", "push"} {
		endpoint := endpoints[channel]
		if endpoint == "" {
			endpoint = endpoints["default"]
		}
		senders[channel] = NewWebhookSender(channel, endpoint)
	}
	return &Client{senders: senders}
}

func NewWebhookSender(channel, endpoint string) *WebhookSender {
	return &WebhookSender{
		channel:    channel,
		endpoint:   endpoint,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

func (c *Client) Send(ctx context.Context, req DeliveryRequest) (DeliveryResponse, int, error) {
	sender := c.senders[req.Channel]
	if sender == nil {
		sender = c.senders["default"]
	}
	if sender == nil {
		return DeliveryResponse{}, 0, fmt.Errorf("provider not configured for channel %s", req.Channel)
	}
	return sender.Send(ctx, req)
}

func (s *WebhookSender) Send(ctx context.Context, req DeliveryRequest) (DeliveryResponse, int, error) {
	ctx, span := tracer.Start(ctx, "provider.send",
		trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(
			attribute.String("notification.channel", req.Channel),
			attribute.String("provider.channel", s.channel),
			attribute.String("provider.endpoint", s.endpoint),
		),
	)
	defer span.End()

	body, err := json.Marshal(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return DeliveryResponse{}, 0, err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return DeliveryResponse{}, 0, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	otel.GetTextMapPropagator().Inject(ctx, propagation.HeaderCarrier(httpReq.Header))

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return DeliveryResponse{}, 0, err
	}
	defer resp.Body.Close()
	span.SetAttributes(attribute.Int("http.response.status_code", resp.StatusCode))
	if resp.StatusCode >= http.StatusInternalServerError {
		span.SetStatus(codes.Error, http.StatusText(resp.StatusCode))
	}

	var payload DeliveryResponse
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return DeliveryResponse{}, resp.StatusCode, fmt.Errorf("decode provider response: %w", err)
	}
	span.SetAttributes(
		attribute.String("provider.message_id", payload.MessageID),
		attribute.String("provider.status", payload.Status),
	)
	return payload, resp.StatusCode, nil
}
