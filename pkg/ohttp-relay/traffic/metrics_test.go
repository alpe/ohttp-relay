package traffic

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRedisTrafficMetrics_RecordTraffic(t *testing.T) {
	// Start mini Redis server
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	metrics, err := NewRedisTrafficMetrics(client, 1*time.Hour)
	require.NoError(t, err)
	require.NotNil(t, metrics)

	ctx := context.Background()
	host := "example.com"
	bytesIn := int64(1024)
	bytesOut := int64(2048)

	// Record traffic
	err = metrics.RecordTraffic(ctx, host, bytesIn, bytesOut)
	require.NoError(t, err)

	// Verify the metrics were recorded correctly
	key := metrics.generateKey(host, time.Now())

	// Check bytes_in
	inVal := mr.HGet(key, "bytes_in")
	assert.Equal(t, "1024", inVal)

	// Check bytes_out
	outVal := mr.HGet(key, "bytes_out")
	assert.Equal(t, "2048", outVal)

	// Verify TTL is set
	ttl := mr.TTL(key)
	assert.Greater(t, ttl, time.Duration(0))
	assert.LessOrEqual(t, ttl, 1*time.Hour)
}

func TestRedisTrafficMetrics_AccumulateTraffic(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	metrics, err := NewRedisTrafficMetrics(client, 1*time.Hour)
	require.NoError(t, err)

	ctx := context.Background()
	host := "example.com"

	// Record traffic multiple times
	err = metrics.RecordTraffic(ctx, host, 100, 200)
	require.NoError(t, err)

	err = metrics.RecordTraffic(ctx, host, 150, 250)
	require.NoError(t, err)

	err = metrics.RecordTraffic(ctx, host, 50, 100)
	require.NoError(t, err)

	// Verify accumulated metrics
	key := metrics.generateKey(host, time.Now())

	inVal := mr.HGet(key, "bytes_in")
	assert.Equal(t, "300", inVal) // 100 + 150 + 50

	outVal := mr.HGet(key, "bytes_out")
	assert.Equal(t, "550", outVal) // 200 + 250 + 100
}

func TestRedisTrafficMetrics_DifferentHosts(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	metrics, err := NewRedisTrafficMetrics(client, 1*time.Hour)
	require.NoError(t, err)

	ctx := context.Background()

	// Record traffic for different hosts
	err = metrics.RecordTraffic(ctx, "host1.com", 100, 200)
	require.NoError(t, err)

	err = metrics.RecordTraffic(ctx, "host2.com", 300, 400)
	require.NoError(t, err)

	// Verify each host has separate metrics
	key1 := metrics.generateKey("host1.com", time.Now())
	key2 := metrics.generateKey("host2.com", time.Now())

	assert.Equal(t, "100", mr.HGet(key1, "bytes_in"))
	assert.Equal(t, "200", mr.HGet(key1, "bytes_out"))

	assert.Equal(t, "300", mr.HGet(key2, "bytes_in"))
	assert.Equal(t, "400", mr.HGet(key2, "bytes_out"))
}

func TestRedisTrafficMetrics_GenerateKey(t *testing.T) {
	metrics := &RedisTrafficMetrics{}

	testTime := time.Date(2024, 3, 15, 14, 30, 0, 0, time.UTC)
	key := metrics.generateKey("example.com", testTime)

	// Expected format: gateway:example.com:traffic:2024-03-15-14
	expected := "gateway:example.com:traffic:2024-03-15-14"
	assert.Equal(t, expected, key)
}

func TestRedisTrafficMetrics_NilMetrics(t *testing.T) {
	var metrics *RedisTrafficMetrics

	ctx := context.Background()
	// Should not panic and should return nil
	err := metrics.RecordTraffic(ctx, "example.com", 100, 200)
	assert.NoError(t, err)
}

func TestRedisTrafficMetrics_ConnectionError(t *testing.T) {
	// Try to connect to a non-existent Redis server
	client := redis.NewClient(&redis.Options{
		Addr: "localhost:9999", // Invalid address
	})
	_, err := NewRedisTrafficMetrics(client, 1*time.Hour)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "redis ping failed")
}

func TestRedisTrafficMetrics_RedisError(t *testing.T) {
	mr := miniredis.RunT(t)

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	metrics, err := NewRedisTrafficMetrics(client, 1*time.Hour)
	require.NoError(t, err)

	// Close the mini Redis to simulate connection failure
	mr.Close()

	ctx := context.Background()
	err = metrics.RecordTraffic(ctx, "example.com", 100, 200)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to record traffic metrics")
}

func TestNoOpTrafficMetrics(t *testing.T) {
	metrics := &NoOpTrafficMetrics{}

	ctx := context.Background()
	err := metrics.RecordTraffic(ctx, "example.com", 100, 200)
	assert.NoError(t, err)
}

func TestRedisTrafficMetrics_TimeWindows(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	// Create a client directly to manipulate time
	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	metrics := &RedisTrafficMetrics{
		rdb: client,
		ttl: 1 * time.Hour,
	}

	ctx := context.Background()

	// Record traffic in different time windows
	now := time.Now()
	oneHourAgo := now.Add(-1 * time.Hour)

	// Simulate recording in current hour
	key1 := metrics.generateKey("example.com", now)
	err := client.HIncrBy(ctx, key1, "bytes_in", 100).Err()
	require.NoError(t, err)

	// Simulate recording in previous hour
	key2 := metrics.generateKey("example.com", oneHourAgo)
	err = client.HIncrBy(ctx, key2, "bytes_in", 200).Err()
	require.NoError(t, err)

	// Verify they're different keys
	assert.NotEqual(t, key1, key2)

	// Verify both have correct values
	val1, err := client.HGet(ctx, key1, "bytes_in").Result()
	require.NoError(t, err)
	assert.Equal(t, "100", val1)

	val2, err := client.HGet(ctx, key2, "bytes_in").Result()
	require.NoError(t, err)
	assert.Equal(t, "200", val2)
}

func TestRedisTrafficMetrics_ConcurrentAccess(t *testing.T) {
	mr := miniredis.RunT(t)
	defer mr.Close()

	client := redis.NewClient(&redis.Options{
		Addr: mr.Addr(),
	})
	defer client.Close()

	metrics, err := NewRedisTrafficMetrics(client, 1*time.Hour)
	require.NoError(t, err)

	ctx := context.Background()
	host := "example.com"

	// Simulate concurrent traffic recording
	numGoroutines := 10
	done := make(chan bool, numGoroutines)

	for i := 0; i < numGoroutines; i++ {
		go func() {
			err := metrics.RecordTraffic(ctx, host, 10, 20)
			assert.NoError(t, err)
			done <- true
		}()
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-done
	}

	// Verify accumulated metrics (should be atomic)
	key := metrics.generateKey(host, time.Now())

	inVal := mr.HGet(key, "bytes_in")
	assert.Equal(t, fmt.Sprintf("%d", 10*numGoroutines), inVal)

	outVal := mr.HGet(key, "bytes_out")
	assert.Equal(t, fmt.Sprintf("%d", 20*numGoroutines), outVal)
}
