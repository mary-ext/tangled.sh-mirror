package telemetry

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/attribute"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	"go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.17.0"
	oteltrace "go.opentelemetry.io/otel/trace"
)

type Telemetry struct {
	tp *trace.TracerProvider
	mp *metric.MeterProvider

	meter  otelmetric.Meter
	tracer oteltrace.Tracer

	serviceName    string
	serviceVersion string
}

func NewTelemetry(ctx context.Context, serviceName, serviceVersion string, isDev bool) (*Telemetry, error) {
	res := resource.NewWithAttributes(
		semconv.SchemaURL,
		semconv.ServiceName(serviceName),
		semconv.ServiceVersion(serviceVersion),
	)

	tp, err := NewTracerProvider(ctx, res, isDev)
	if err != nil {
		return nil, err
	}

	mp, err := NewMeterProvider(ctx, res, isDev)
	if err != nil {
		return nil, err
	}

	return &Telemetry{
		tp: tp,
		mp: mp,

		meter:  mp.Meter(serviceName),
		tracer: tp.Tracer(serviceVersion),

		serviceName:    serviceName,
		serviceVersion: serviceVersion,
	}, nil
}

func (t *Telemetry) Meter() otelmetric.Meter {
	return t.meter
}

func (t *Telemetry) Tracer() oteltrace.Tracer {
	return t.tracer
}

func (t *Telemetry) TraceStart(ctx context.Context, name string, attrs ...attribute.KeyValue) (context.Context, oteltrace.Span) {
	ctx, span := t.tracer.Start(ctx, name)
	span.SetAttributes(attrs...)
	return ctx, span
}

func MapAttrs[T any](attrs map[string]T) []attribute.KeyValue {
	var result []attribute.KeyValue
	for k, v := range attrs {
		result = append(result, attribute.Key(k).String(fmt.Sprintf("%v", v)))
	}
	return result
}
