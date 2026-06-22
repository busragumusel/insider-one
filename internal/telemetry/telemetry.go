package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	metricgrpc "go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"insider-one/internal/config"
)

type Telemetry struct {
	metricProvider *sdkmetric.MeterProvider
	traceProvider  *sdktrace.TracerProvider
	shutdown       func(context.Context) error
}

func New(ctx context.Context, cfg config.Config) (*Telemetry, error) {
	if !cfg.OtelEnabled {
		return &Telemetry{}, nil
	}

	metricExporter, err := metricgrpc.New(ctx,
		metricgrpc.WithEndpoint(cfg.OtelEndpoint),
		metricgrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("create otlp metric exporter: %w", err)
	}
	traceExporter, err := otlptracegrpc.New(ctx,
		otlptracegrpc.WithEndpoint(cfg.OtelEndpoint),
		otlptracegrpc.WithInsecure(),
	)
	if err != nil {
		return nil, fmt.Errorf("create otlp trace exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			attribute.String("service.name", cfg.ServiceName),
			attribute.String("service.namespace", "insider-one"),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create resource: %w", err)
	}

	metricProvider := sdkmetric.NewMeterProvider(
		sdkmetric.WithResource(res),
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter)),
	)
	traceProvider := sdktrace.NewTracerProvider(
		sdktrace.WithResource(res),
		sdktrace.WithBatcher(traceExporter),
	)
	otel.SetMeterProvider(metricProvider)
	otel.SetTracerProvider(traceProvider)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(propagation.TraceContext{}, propagation.Baggage{}))

	return &Telemetry{
		metricProvider: metricProvider,
		traceProvider:  traceProvider,
		shutdown: func(ctx context.Context) error {
			metricErr := metricProvider.Shutdown(ctx)
			traceErr := traceProvider.Shutdown(ctx)
			if metricErr != nil {
				return metricErr
			}
			return traceErr
		},
	}, nil
}

func (t *Telemetry) Close(ctx context.Context) error {
	if t == nil || t.shutdown == nil {
		return nil
	}
	return t.shutdown(ctx)
}
