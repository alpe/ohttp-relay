package metrics

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

const (
	instrumentationName = "github.com/alpe/ohttprelay/pkg/ohttprelay/metrics"
)

var (
	// Counters
	RequestsTotal        metric.Int64Counter
	ErrorsTotal          metric.Int64Counter
	GatewayRequestsTotal metric.Int64Counter

	// Histograms
	RequestDuration        metric.Float64Histogram
	GatewayRequestDuration metric.Float64Histogram
	RequestSizeBytes       metric.Int64Histogram
	ResponseSizeBytes      metric.Int64Histogram

	// Gauges
	ActiveRequests metric.Int64UpDownCounter
)

// InitMetrics initializes the OpenTelemetry metrics with a Prometheus exporter.
func InitMetrics() error {
	exporter, err := otelprom.New()
	if err != nil {
		return fmt.Errorf("failed to create prometheus exporter: %w", err)
	}

	provider := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(exporter),
	)
	otel.SetMeterProvider(provider)

	meter := provider.Meter(instrumentationName)

	// Initialize instruments
	var initErr error

	RequestsTotal, initErr = meter.Int64Counter(
		"ohttp_relay_requests_total",
		metric.WithDescription("Total requests processed"),
	)
	if initErr != nil {
		return initErr
	}

	ErrorsTotal, initErr = meter.Int64Counter(
		"ohttp_relay_errors_total",
		metric.WithDescription("Total errors encountered"),
	)
	if initErr != nil {
		return initErr
	}

	GatewayRequestsTotal, initErr = meter.Int64Counter(
		"ohttp_relay_gateway_requests_total",
		metric.WithDescription("Total gateway requests"),
	)
	if initErr != nil {
		return initErr
	}

	RequestDuration, initErr = meter.Float64Histogram(
		"ohttp_relay_request_duration_seconds",
		metric.WithDescription("Request processing duration"),
		metric.WithUnit("s"),
	)
	if initErr != nil {
		return initErr
	}

	GatewayRequestDuration, initErr = meter.Float64Histogram(
		"ohttp_relay_gateway_duration_seconds",
		metric.WithDescription("Gateway request duration"),
		metric.WithUnit("s"),
	)
	if initErr != nil {
		return initErr
	}

	RequestSizeBytes, initErr = meter.Int64Histogram(
		"ohttp_relay_request_size_bytes",
		metric.WithDescription("Request body size distribution"),
		metric.WithUnit("By"),
	)
	if initErr != nil {
		return initErr
	}

	ResponseSizeBytes, initErr = meter.Int64Histogram(
		"ohttp_relay_response_size_bytes",
		metric.WithDescription("Response body size distribution"),
		metric.WithUnit("By"),
	)
	if initErr != nil {
		return initErr
	}

	ActiveRequests, initErr = meter.Int64UpDownCounter(
		"ohttp_relay_active_requests",
		metric.WithDescription("Current number of requests being processed"),
	)
	if initErr != nil {
		return initErr
	}

	return nil
}

// RecordRequest records metrics for a completed request
func RecordRequest(ctx context.Context, method, statusCategory string, duration time.Duration, reqSize, respSize int64) {
	attrs := metric.WithAttributes(
		attribute.String("method", method),
		attribute.String("status_category", statusCategory),
	)

	RequestsTotal.Add(ctx, 1, attrs)
	RequestDuration.Record(ctx, duration.Seconds(), attrs)
	RequestSizeBytes.Record(ctx, reqSize, attrs)
	ResponseSizeBytes.Record(ctx, respSize, attrs)
}

// RecordError records an error metric
func RecordError(ctx context.Context, errorType string) {
	ErrorsTotal.Add(ctx, 1, metric.WithAttributes(
		attribute.String("error_type", errorType),
	))
}

// RecordGatewayRequest records metrics for a gateway request
func RecordGatewayRequest(ctx context.Context, statusCode int, duration time.Duration) {
	statusCategory := fmt.Sprintf("%dxx", statusCode/100)
	attrs := metric.WithAttributes(
		attribute.String("status_category", statusCategory),
	)

	GatewayRequestsTotal.Add(ctx, 1, attrs)
	GatewayRequestDuration.Record(ctx, duration.Seconds(), attrs)
}
