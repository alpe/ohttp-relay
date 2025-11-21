package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alpe/ohttp-relay/internal/ctrl"
	"github.com/alpe/ohttp-relay/internal/runnable"
	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/gateway"
	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/metrics"
	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/relay"
	runserver "github.com/alpe/ohttp-relay/pkg/ohttp-relay/server"
	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/util/logging"
	"github.com/go-logr/zapr"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	uberzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	healthPb "google.golang.org/grpc/health/grpc_health_v1"
)

var (
	grpcPort       = flag.Int("grpc-port", 9006, "The gRPC port used for communicating with Envoy proxy")
	grpcHealthPort = flag.Int("grpc-health-port", 9007, "The port used for gRPC liveness and readiness probes")
	metricsPort    = flag.Int("metrics-port", 9090, "The metrics port")
	gatewayURLs    = flag.String("gateway-urls", "", "Comma-separated domain:url mappings for OHTTP gateway, e.g. domain:https://gateway/relay,other:https://other/relay")
	defaultGateway = flag.String("default-gateway-url", "", "default gateway DestinationURL to use when no mapping is found for a host")
	// Redis-backed gateway source flags
	redisEnable   = flag.Bool("redis-enable", false, "Enable Redis as dynamic gateway source")
	redisAddr     = flag.String("redis-addr", "", "Redis address host:port (enables Redis source when set with --redis-enable)")
	redisPassword = flag.String("redis-password", "", "Redis password")
	redisDB       = flag.Int("redis-db", 0, "Redis DB number")
	redisTLS      = flag.Bool("redis-tls", false, "Use TLS for Redis connection")

	timeoutFlag  = flag.Duration("timeout", 9*time.Second, "HTTP timeout when relaying to the OHTTP gateway")
	maxBodySize  = flag.Int64("max-request-body-size", 10*1024*1024, "Maximum allowed request body size in bytes (default 10MB)")
	logVerbosity = flag.Int("v", logging.DEFAULT, "number for the log level verbosity")

	setupLog = ctrl.Log.WithName("setup")
)

func main() {
	if err := run(); err != nil {
		os.Exit(1)
	}
}

func run() error {
	flag.Parse()
	initLogging()

	if envPass := os.Getenv("REDIS_PASSWORD"); envPass != "" {
		*redisPassword = envPass
	}

	// Print all flag values
	flags := make(map[string]any)
	flag.VisitAll(func(f *flag.Flag) {
		if strings.Contains(strings.ToLower(f.Name), "password") {
			return
		}
		flags[f.Name] = f.Value
	})
	setupLog.Info("Flags processed", "flags", flags)

	if err := metrics.InitMetrics(); err != nil {
		return fmt.Errorf("init metrics: %w", err)
	}

	wg, ctx := errgroup.WithContext(ctrl.SetupSignalHandler())
	srv := grpc.NewServer()
	healthPb.RegisterHealthServer(srv, &healthServer{logger: ctrl.Log.WithName("health")})
	wg.Go(func() error { return runnable.GRPCServer("health", srv, *grpcHealthPort)(ctx) })

	// Start metrics server
	wg.Go(func() error {
		setupLog.Info("Starting metrics server", "port", *metricsPort)
		mux := http.NewServeMux()
		mux.Handle("/metrics", promhttp.Handler())
		server := &http.Server{
			Addr:    fmt.Sprintf(":%d", *metricsPort),
			Handler: mux,
		}

		errChan := make(chan error, 1)
		go func() {
			if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				errChan <- err
			}
			close(errChan)
		}()

		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := server.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("metrics server shutdown failed: %w", err)
			}
			return nil
		case err := <-errChan:
			return fmt.Errorf("metrics server failed: %w", err)
		}
	})

	wg.Go(func() error {
		logger := ctrl.Log.WithName("ext-proc")
		var gwSource gateway.Source
		if *redisEnable && strings.TrimSpace(*redisAddr) != "" {
			setupLog.Info("Using Redis gateway source", "addr", *redisAddr, "db", *redisDB, "tls", *redisTLS)
			var err error
			gwSource, err = gateway.NewRedisGatewaySource(gateway.RedisOptions{
				Addr:     *redisAddr,
				Password: *redisPassword, // Password from flag or environment
				DB:       *redisDB,
				UseTLS:   *redisTLS,
				Timeout:  *timeoutFlag,
			})
			if err != nil {
				return fmt.Errorf("initialize Redis gateway source: %w", err)
			}
		} else {
			domainMap, err := parseDomainMap(*gatewayURLs)
			if err != nil {
				return fmt.Errorf("parse domain map: %w", err)
			}
			setupLog.Info("Domain map", "domainMap", domainMap)
			gwSource = gateway.StaticMapSource(domainMap)
		}
		fallbackGWSource := gateway.FallbackSource(*defaultGateway, gwSource)
		relayer := relay.NewHTTPRelayer(fallbackGWSource, *timeoutFlag)
		serverRunner := runserver.NewExtProcServerRunner(*grpcPort, relayer, *maxBodySize, logger)
		return serverRunner.Start(ctx)
	})

	setupLog.Info("Relay starting")
	if err := wg.Wait(); err != nil {
		setupLog.Error(err, "Error starting relay")
		return err
	}
	setupLog.Info("Relay terminated")
	return nil
}

func initLogging() {
	// Map our verbosity flag to zap levels: higher v => more verbose => lower zap level
	lvl := -1 * (*logVerbosity)
	atom := uberzap.NewAtomicLevelAt(zapcore.Level(int8(lvl)))
	cfg := uberzap.NewProductionConfig()
	cfg.Level = atom
	// Build logger
	zl, err := cfg.Build(uberzap.AddCaller())
	if err != nil {
		// Fallback to default
		zl = uberzap.NewNop()
	}
	ctrl.SetLogger(zapr.NewLogger(zl))
	setupLog = ctrl.Log.WithName("setup")
}

func parseDomainMap(spec string) (map[string]string, error) {
	m := map[string]string{}
	if strings.TrimSpace(spec) == "" {
		return m, nil
	}
	pairs := strings.Split(spec, ",")
	for _, p := range pairs {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		kv := strings.SplitN(p, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid mapping %q, expected domain:url", p)
		}
		domain := strings.ToLower(strings.TrimSpace(kv[0]))
		url := strings.TrimSpace(kv[1])
		if domain == "" || url == "" {
			return nil, fmt.Errorf("invalid mapping %q, empty domain or url", p)
		}
		m[domain] = url
	}
	return m, nil
}
