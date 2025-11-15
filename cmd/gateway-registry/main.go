package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/alpe/ohttprelay/internal/ctrl"
	"github.com/redis/go-redis/v9"
)

const (
	defaultKey = "gateway:url"
)

func main() {
	var (
		redisAddr      = flag.String("redis-addr", envOr("REDIS_ADDR", "redis:6379"), "Redis address host:port")
		redisPassword  = flag.String("redis-password", envOr("REDIS_PASSWORD", ""), "Redis password")
		redisDB        = flag.Int("redis-db", envOrInt("REDIS_DB", 0), "Redis DB number")
		redisTLS       = flag.Bool("redis-tls", envOrBool("REDIS_TLS", false), "Use TLS for Redis connection")
		defaultGateway = flag.String("default-gateway-url", "", "default gateway DestinationURL to use when no mapping is found for a host")

		listenAddr = flag.String("listen", envOr("LISTEN_ADDR", ":8080"), "Listen address for HTTP server")
	)
	flag.Parse()

	if envPass := os.Getenv("REDIS_PASSWORD"); envPass != "" {
		*redisPassword = envPass
	}

	// Setup Redis client
	opt := &redis.Options{Addr: *redisAddr, Password: *redisPassword, DB: *redisDB}
	if *redisTLS {
		// Use a simple TLS config relying on system roots.
		opt.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12}
	}
	rdb := redis.NewClient(opt)
	ctx := ctrl.SetupSignalHandler()
	pCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := rdb.Ping(pCtx).Err(); err != nil {
		log.Fatalf("failed to connect to redis at %s: %v", *redisAddr, err)
	}

	// Server mode
	if defaultGateway == nil || *defaultGateway == "" {
		log.Fatalf("default gateway DestinationURL must be specified")
	}
	s := newServer(rdb, *listenAddr, 24*time.Hour, *defaultGateway)
	log.Printf("gateway-registry HTTP server listening on %s (redis=%s)", *listenAddr, *redisAddr)

	go func() {
		if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("http server error: %v", err)
		}
	}()

	<-ctx.Done()
	log.Println("shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := s.srv.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("server shutdown failed: %v", err)
	}
	log.Println("server gracefully stopped")
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func envOrInt(key string, def int) int {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		var out int
		_, err := fmt.Sscanf(v, "%d", &out)
		if err == nil {
			return out
		}
	}
	return def
}

func envOrBool(key string, def bool) bool {
	if v := strings.TrimSpace(strings.ToLower(os.Getenv(key))); v != "" {
		switch v {
		case "1", "true", "yes", "y":
			return true
		case "0", "false", "no", "n":
			return false
		}
	}
	return def
}
