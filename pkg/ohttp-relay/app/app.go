package app

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/alpe/ohttp-relay/internal/ctrl"
	"github.com/alpe/ohttp-relay/internal/runnable"
	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/config"
	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/gateway"
	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/metrics"
	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/relay"
	runserver "github.com/alpe/ohttp-relay/pkg/ohttp-relay/server"
	"github.com/alpe/ohttp-relay/pkg/ohttp-relay/traffic"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/redis/go-redis/v9"
	uberzap "go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"
	healthPb "google.golang.org/grpc/health/grpc_health_v1"
)

type App struct {
	cfg    *config.Config
	logger logr.Logger
}

func New(cfg *config.Config) *App {
	initLogging(cfg.LogVerbosity)
	return &App{
		cfg:    cfg,
		logger: ctrl.Log.WithName("app"),
	}
}

func (a *App) Run(ctx context.Context) error {
	a.logger.Info("Starting application", "config", a.cfg) // Be careful not to log sensitive info if added later

	if err := metrics.InitMetrics(); err != nil {
		return fmt.Errorf("init metrics: %w", err)
	}

	wg, ctx := errgroup.WithContext(ctx)

	// Health Server
	srv := grpc.NewServer()
	healthPb.RegisterHealthServer(srv, &healthServer{logger: ctrl.Log.WithName("health")})
	wg.Go(func() error { return runnable.GRPCServer("health", srv, a.cfg.GRPCHealthPort)(ctx) })

	// Metrics Server
	wg.Go(func() error {
		return a.runMetricsServer(ctx)
	})

	// Main Relay Server
	wg.Go(func() error {
		return a.runRelayServer(ctx)
	})

	a.logger.Info("Relay starting")
	if err := wg.Wait(); err != nil {
		a.logger.Error(err, "Error starting relay")
		return err
	}
	a.logger.Info("App terminated")
	return nil
}

func (a *App) runMetricsServer(ctx context.Context) error {
	addr := a.cfg.MetricsAddr
	if strings.TrimSpace(addr) == "" {
		addr = fmt.Sprintf(":%d", a.cfg.MetricsPort)
	}
	a.logger.Info("Starting metrics server", "addr", addr)
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	server := &http.Server{
		Addr:    addr,
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
}

func (a *App) runRelayServer(ctx context.Context) error {
	logger := ctrl.Log.WithName("ext-proc")

	staticSource := gateway.StaticMapURLSource(a.cfg.DomainMap)
	var gwSource gateway.URLSource
	var trafficMetrics traffic.TrafficMetrics

	if a.cfg.RedisEnable && strings.TrimSpace(a.cfg.RedisAddr) != "" {
		ro := &redis.Options{
			Addr:     a.cfg.RedisAddr,
			Password: a.cfg.RedisPassword,
			DB:       a.cfg.RedisDB,
		}
		if a.cfg.RedisTLS {
			ro.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
		}

		redisClient := redis.NewClient(ro)
		// Validate connectivity
		pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if err := redisClient.Ping(pingCtx).Err(); err != nil {
			return fmt.Errorf("redis ping failed: %w", err)
		}

		a.logger.Info("Using Redis gateway source", "addr", a.cfg.RedisAddr)
		redisSource, err := gateway.NewRedisGatewaySource(redisClient, a.cfg.Timeout)
		if err != nil {
			return fmt.Errorf("initialize Redis gateway source: %w", err)
		}
		gwSource = gateway.ChainSources(staticSource, redisSource)

		a.logger.Info("Enabling traffic metrics with Redis")
		tm, err := traffic.NewRedisTrafficMetrics(redisClient, 72*time.Hour)
		if err != nil {
			return fmt.Errorf("initialize Redis traffic metrics: %w", err)
		}
		trafficMetrics = tm
	} else {
		gwSource = staticSource
		a.logger.Info("Traffic metrics disabled (Redis not enabled)")
		trafficMetrics = &traffic.NoOpTrafficMetrics{}
	}

	httpClient := &http.Client{Timeout: a.cfg.Timeout, Transport: http.DefaultTransport}
	relayer := relay.NewHTTPRelayer(httpClient, gwSource)
	ks := gateway.NewHttpKeyConfigSource(httpClient, gwSource)
	serverRunner := runserver.NewExtProcServerRunner(a.cfg.GRPCPort, relayer, ks, a.cfg.MaxRequestBodySize, trafficMetrics, logger)
	return serverRunner.Start(ctx)
}

func initLogging(verbosity int) {
	lvl := -1 * (verbosity)
	atom := uberzap.NewAtomicLevelAt(zapcore.Level(int8(lvl)))
	cfg := uberzap.NewProductionConfig()
	cfg.Level = atom
	zl, err := cfg.Build(uberzap.AddCaller())
	if err != nil {
		zl = uberzap.NewNop()
	}
	ctrl.SetLogger(zapr.NewLogger(zl))
}

// healthServer simple implementation for gRPC health checks
type healthServer struct {
	healthPb.UnimplementedHealthServer
	logger logr.Logger
}

func (s *healthServer) Check(ctx context.Context, req *healthPb.HealthCheckRequest) (*healthPb.HealthCheckResponse, error) {
	return &healthPb.HealthCheckResponse{Status: healthPb.HealthCheckResponse_SERVING}, nil
}

func (s *healthServer) Watch(req *healthPb.HealthCheckRequest, ws healthPb.Health_WatchServer) error {
	return ws.Send(&healthPb.HealthCheckResponse{Status: healthPb.HealthCheckResponse_SERVING})
}
