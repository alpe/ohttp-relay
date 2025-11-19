package handlers

import (
	"os"
	"testing"

	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/metrics"
)

func TestMain(m *testing.M) {
	// Initialize metrics before running tests to avoid panics
	// when accessing global metric variables.
	_ = metrics.InitMetrics()
	os.Exit(m.Run())
}
