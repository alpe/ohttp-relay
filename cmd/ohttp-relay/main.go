package main

import (
	"context"
	"fmt"
	"os"

	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/app"
	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/config"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("failed to load config: %w", err)
	}

	a := app.New(cfg)
	return a.Run(context.Background())
}
