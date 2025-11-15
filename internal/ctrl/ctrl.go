package ctrl

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/go-logr/logr"
)

// Log is the global logger used across the application. Set it at startup.
var Log = logr.Discard()

// SetLogger sets the global logger.
func SetLogger(l logr.Logger) { Log = l }

// FromContext returns the global logger. This project does not thread loggers via context.
func FromContext(ctx context.Context) logr.Logger { return Log }

// SetupSignalHandler returns a context that is cancelled on SIGINT or SIGTERM.
// A second signal causes immediate termination.
func SetupSignalHandler() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	c := make(chan os.Signal, 2)
	signal.Notify(c, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-c
		cancel()
		// If we receive another signal, exit immediately.
		<-c
		os.Exit(1)
	}()
	return ctx
}
