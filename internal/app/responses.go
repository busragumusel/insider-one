package app

import (
	"net/http"
	"time"

	"insider-one/internal/domain"
	"insider-one/internal/httpx"
)

type responseMeta struct {
	RequestID     string `json:"requestId"`
	CorrelationID string `json:"correlationId"`
	Timestamp     string `json:"timestamp"`
}

type notificationEnvelope struct {
	Notification domain.Notification `json:"notification"`
	Meta         responseMeta        `json:"meta"`
}

type batchEnvelope struct {
	BatchID       string                `json:"batchId"`
	Count         int                   `json:"count"`
	Notifications []domain.Notification `json:"notifications"`
	Meta          responseMeta          `json:"meta"`
}

type listEnvelope struct {
	Notifications []domain.Notification `json:"notifications"`
	Pagination    paginationMeta        `json:"pagination"`
	Meta          responseMeta          `json:"meta"`
}

type eventListEnvelope struct {
	Events     []domain.DeliveryEvent `json:"events"`
	Pagination paginationMeta         `json:"pagination"`
	Meta       responseMeta           `json:"meta"`
}

type cancelEnvelope struct {
	Cancelled int64        `json:"cancelled"`
	IDs       []string     `json:"ids,omitempty"`
	Meta      responseMeta `json:"meta"`
}

type healthEnvelope struct {
	Status   string       `json:"status"`
	Service  string       `json:"service"`
	UptimeMs int64        `json:"uptimeMs"`
	Meta     responseMeta `json:"meta"`
}

type errorEnvelope struct {
	Error string       `json:"error"`
	Code  string       `json:"code"`
	Meta  responseMeta `json:"meta"`
}

type paginationMeta struct {
	Limit    int  `json:"limit"`
	Offset   int  `json:"offset"`
	Returned int  `json:"returned"`
	HasMore  bool `json:"hasMore"`
}

func buildResponseMeta(r *http.Request) responseMeta {
	return responseMeta{
		RequestID:     httpx.RequestID(r.Context()),
		CorrelationID: httpx.CorrelationID(r.Context()),
		Timestamp:     time.Now().UTC().Format(time.RFC3339Nano),
	}
}
