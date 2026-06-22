package domain

import "time"

type Channel string

const (
	ChannelSMS   Channel = "sms"
	ChannelEmail Channel = "email"
	ChannelPush  Channel = "push"
)

type Priority string

const (
	PriorityHigh   Priority = "high"
	PriorityNormal Priority = "normal"
	PriorityLow    Priority = "low"
)

type Status string

const (
	StatusPending    Status = "pending"
	StatusProcessing Status = "processing"
	StatusDelivered  Status = "delivered"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
	StatusScheduled  Status = "scheduled"
	StatusDeadLetter Status = "dead_letter"
)

type DeliveryEvent struct {
	ID                int64      `json:"id"`
	TenantID          string     `json:"tenantId,omitempty"`
	NotificationID    string     `json:"notificationId"`
	BatchID           string     `json:"batchId,omitempty"`
	Channel           Channel    `json:"channel"`
	Status            Status     `json:"status"`
	Attempt           int        `json:"attempt"`
	Reason            string     `json:"reason,omitempty"`
	NextAttemptAt     *time.Time `json:"nextAttemptAt,omitempty"`
	ProviderStatus    int        `json:"providerStatus,omitempty"`
	ProviderMessageID string     `json:"providerMessageId,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
}

type Notification struct {
	ID                string     `json:"id"`
	TenantID          string     `json:"tenantId,omitempty"`
	BatchID           string     `json:"batchId,omitempty"`
	Recipient         string     `json:"recipient"`
	Channel           Channel    `json:"channel"`
	Content           string     `json:"content"`
	Priority          Priority   `json:"priority"`
	Status            Status     `json:"status"`
	Attempts          int        `json:"attempts"`
	MaxAttempts       int        `json:"maxAttempts"`
	ScheduledAt       *time.Time `json:"scheduledAt,omitempty"`
	CreatedAt         time.Time  `json:"createdAt"`
	UpdatedAt         time.Time  `json:"updatedAt"`
	CorrelationID     string     `json:"correlationId,omitempty"`
	IdempotencyKey    string     `json:"idempotencyKey,omitempty"`
	ProviderMessageID string     `json:"providerMessageId,omitempty"`
	LastError         string     `json:"lastError,omitempty"`
}

type NotificationRequest struct {
	Recipient      string     `json:"recipient"`
	Channel        Channel    `json:"channel"`
	Content        string     `json:"content"`
	Priority       Priority   `json:"priority"`
	ScheduledAt    *time.Time `json:"scheduledAt,omitempty"`
	IdempotencyKey string     `json:"idempotencyKey,omitempty"`
}

type NotificationBatchRequest struct {
	Notifications  []NotificationRequest `json:"notifications"`
	IdempotencyKey string                `json:"idempotencyKey,omitempty"`
}
