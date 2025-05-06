package telemetry

import (
	"fmt"
	"net/http"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	otelmetric "go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/semconv/v1.13.0/httpconv"
)

func (t *Telemetry) RequestDuration() func(next http.Handler) http.Handler {
	const (
		metricNameRequestDurationMs = "request_duration_millis"
		metricUnitRequestDurationMs = "ms"
		metricDescRequestDurationMs = "Measures the latency of HTTP requests processed by the server, in milliseconds."
	)
	histogram, err := t.meter.Int64Histogram(
		metricNameRequestDurationMs,
		otelmetric.WithDescription(metricDescRequestDurationMs),
		otelmetric.WithUnit(metricUnitRequestDurationMs),
	)
	if err != nil {
		panic(fmt.Sprintf("unable to create %s histogram: %v", metricNameRequestDurationMs, err))
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// capture the start time of the request
			startTime := time.Now()

			// execute next http handler
			next.ServeHTTP(w, r)

			// record the request duration
			duration := time.Since(startTime)
			histogram.Record(
				r.Context(),
				int64(duration.Milliseconds()),
				otelmetric.WithAttributes(
					httpconv.ServerRequest(t.serviceName, r)...,
				),
			)
		})
	}
}

func (t *Telemetry) RequestInFlight() func(next http.Handler) http.Handler {
	const (
		metricNameRequestInFlight = "request_in_flight"
		metricDescRequestInFlight = "Measures the number of concurrent HTTP requests being processed by the server."
		metricUnitRequestInFlight = "1"
	)

	// counter to capture requests in flight
	counter, err := t.meter.Int64UpDownCounter(
		metricNameRequestInFlight,
		otelmetric.WithDescription(metricDescRequestInFlight),
		otelmetric.WithUnit(metricUnitRequestInFlight),
	)
	if err != nil {
		panic(fmt.Sprintf("unable to create %s counter: %v", metricNameRequestInFlight, err))
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			attrs := otelmetric.WithAttributes(httpconv.ServerRequest(t.serviceName, r)...)

			// increase the number of requests in flight
			counter.Add(r.Context(), 1, attrs)

			// execute next http handler
			next.ServeHTTP(w, r)

			// decrease the number of requests in flight
			counter.Add(r.Context(), -1, attrs)
		})
	}
}

func (t *Telemetry) WithRouteTag() func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			otelhttp.WithRouteTag(r.URL.Path, next)
		})
	}
}
