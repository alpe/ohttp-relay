package handlers

import (
	"os"
	"testing"

	"github.com/alpe/ohttprelay/pkg/ohttprelay/metrics"
)

func TestMain(m *testing.M) {
	// Initialize metrics before running tests to avoid panics
	// when accessing global metric variables.
	_ = metrics.InitMetrics()
	os.Exit(m.Run())
}
