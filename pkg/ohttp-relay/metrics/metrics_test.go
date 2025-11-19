package metrics

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestInitMetrics(t *testing.T) {
	err := InitMetrics()
	assert.NoError(t, err)
}

func TestRecordRequest(t *testing.T) {
	// Ensure metrics are initialized
	_ = InitMetrics()

	// Should not panic
	assert.NotPanics(t, func() {
		RecordRequest(context.Background(), "POST", "2xx", 100*time.Millisecond, 1024, 2048)
	})
}

func TestRecordError(t *testing.T) {
	// Ensure metrics are initialized
	_ = InitMetrics()

	// Should not panic
	assert.NotPanics(t, func() {
		RecordError(context.Background(), "relay_error")
	})
}

func TestRecordGatewayRequest(t *testing.T) {
	// Ensure metrics are initialized
	_ = InitMetrics()

	// Should not panic
	assert.NotPanics(t, func() {
		RecordGatewayRequest(context.Background(), 200, 50*time.Millisecond)
	})
}
