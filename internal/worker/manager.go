package worker

import (
	"context"
	"log/slog"
	"time"
)

type Processor interface {
	ProcessChannel(ctx context.Context, channel string) error
}

type Manager struct {
	processor    Processor
	logger       *slog.Logger
	channels     []string
	concurrency  int
	tickInterval time.Duration
	jobTimeout   time.Duration
}

type Option func(*Manager)

func WithConcurrency(concurrency int) Option {
	return func(m *Manager) {
		if concurrency > 0 {
			m.concurrency = concurrency
		}
	}
}

func New(processor Processor, logger *slog.Logger, opts ...Option) *Manager {
	manager := &Manager{
		processor:    processor,
		logger:       logger,
		channels:     []string{"sms", "email", "push"},
		concurrency:  1,
		tickInterval: 10 * time.Millisecond,
		jobTimeout:   5 * time.Second,
	}
	for _, opt := range opts {
		opt(manager)
	}
	return manager
}

func (m *Manager) Start(ctx context.Context) {
	for _, channel := range m.channels {
		channel := channel
		for workerID := 0; workerID < m.concurrency; workerID++ {
			go m.runChannel(ctx, channel, workerID)
		}
	}
}

func (m *Manager) runChannel(ctx context.Context, channel string, workerID int) {
	ticker := time.NewTicker(m.tickInterval)
	defer ticker.Stop()
	channelLogger := m.logger
	if channelLogger != nil {
		channelLogger = channelLogger.With("channel", channel, "worker_id", workerID)
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			jobCtx, cancel := context.WithTimeout(ctx, m.jobTimeout)
			if err := m.processor.ProcessChannel(jobCtx, channel); err != nil && m.logger != nil {
				channelLogger.Warn("notification worker cycle failed", "err", err)
			}
			cancel()
		}
	}
}
