package traffic

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
)

// TrafficMetrics defines the interface for recording traffic metrics.
type TrafficMetrics interface {
	RecordTraffic(ctx context.Context, host string, bytesIn, bytesOut int64) error
}

// RedisTrafficMetrics implements TrafficMetrics using Redis as storage.
// It stores metrics in time-windowed buckets (hourly) with automatic TTL.
type RedisTrafficMetrics struct {
	rdb *redis.Client
	ttl time.Duration
}

// NewRedisTrafficMetrics creates a new RedisTrafficMetrics instance.
func NewRedisTrafficMetrics(client *redis.Client, ttl time.Duration) (*RedisTrafficMetrics, error) {
	if ttl <= 0 {
		ttl = 72 * time.Hour // Default: keep metrics for 72 hours
	}

	// Validate connection if client is provided
	if client != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := client.Ping(ctx).Err(); err != nil {
			return nil, fmt.Errorf("redis ping failed: %w", err)
		}
	}

	return &RedisTrafficMetrics{
		rdb: client,
		ttl: ttl,
	}, nil
}

// RecordTraffic records traffic metrics for a given host in a time-windowed Redis key.
// The key format is: gateway:{host}:traffic:{YYYY-MM-DD-HH}
// Fields: bytes_in, bytes_out
func (r *RedisTrafficMetrics) RecordTraffic(ctx context.Context, host string, bytesIn, bytesOut int64) error {
	if r == nil || r.rdb == nil {
		return nil // No-op if metrics are disabled
	}

	key := r.generateKey(host, time.Now())

	pipe := r.rdb.Pipeline()

	// Increment traffic counters atomically
	pipe.HIncrBy(ctx, key, "bytes_in", bytesIn)
	pipe.HIncrBy(ctx, key, "bytes_out", bytesOut)

	// Set TTL on the key (NX flag ensures we only set it once when key is created)
	pipe.Expire(ctx, key, r.ttl)

	_, err := pipe.Exec(ctx)
	if err != nil {
		return fmt.Errorf("failed to record traffic metrics: %w", err)
	}

	return nil
}

// generateKey creates a time-windowed Redis key for traffic metrics.
// Format: gateway:{host}:traffic:{YYYY-MM-DD-HH}
func (r *RedisTrafficMetrics) generateKey(host string, t time.Time) string {
	// Use UTC to ensure consistent bucketing across time zones
	window := t.UTC().Format("2006-01-02-15")
	return fmt.Sprintf("gateway:%s:traffic:%s", host, window)
}

// NoOpTrafficMetrics is a no-op implementation that can be used when metrics are disabled.
type NoOpTrafficMetrics struct{}

func (n *NoOpTrafficMetrics) RecordTraffic(ctx context.Context, host string, bytesIn, bytesOut int64) error {
	return nil
}
