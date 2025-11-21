package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/redis/go-redis/v9"
)

// GatewayEntry represents a gateway entry in the registry that is used to route traffic.
type GatewayEntry struct {
	// ID is the unique identifier for this gateway entry. It is used as subdomain
	ID             string `json:"id"`
	DestinationURL string `json:"destination_url"`
	// ExpiryDate is an absolute timestamp (RFC3339) when the entry expires.
	ExpiryDate time.Time `json:"expiryDate"`
}

type server struct {
	defaultGatewayURL string
	rdb               *redis.Client
	defaultTTL        time.Duration
	srv               *http.Server
}

func newServer(rdb *redis.Client, listenAddr string, entryTTL time.Duration, defaultGatewayURL string) *server {
	s := &server{rdb: rdb, defaultTTL: entryTTL, defaultGatewayURL: defaultGatewayURL}

	srv := &http.Server{
		Addr:         listenAddr,
		Handler:      s.routes(),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  60 * time.Second,
	}
	s.srv = srv
	return s
}

func (s *server) handleGetGateway(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := chi.URLParam(r, "id")
	val, err := s.rdb.Get(ctx, id).Result()
	if errors.Is(err, redis.Nil) {
		http.Error(w, "gateway url not set", http.StatusNotFound)
		return
	}
	if err != nil {
		http.Error(w, fmt.Sprintf("redis error: %v", err), http.StatusInternalServerError)
		return
	}
	// Try to read remaining TTL to compute expiryDate, if any
	var expPtr time.Time
	if ttl, err := s.rdb.TTL(ctx, id).Result(); err == nil {
		// redis TTL semantics: -2 key doesn't exist, -1 no expire, >0 has TTL
		if ttl > 0 {
			t := time.Now().UTC().Add(ttl)
			expPtr = t
		}
	}
	writeJSON(w, http.StatusOK, GatewayEntry{ID: id, DestinationURL: val, ExpiryDate: expPtr})
}

func (s *server) handleCreateGateway(w http.ResponseWriter, r *http.Request) {
	authHeader := r.Header.Get("Authorization")
	var userID string
	authorized := false
	if strings.HasPrefix(authHeader, "Bearer ") {
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token != "" {
			authorized = true
			userID = token // In real implementation, validate and decode token
		}
	}

	ctx := r.Context()
	var entry GatewayEntry
	if !authorized || userID == "" {
		// id is generated on store
		entry.DestinationURL = s.defaultGatewayURL
		entry.ExpiryDate = time.Now().UTC().Add(s.defaultTTL)
	} else {
		// todo: implement authorization
		entry.DestinationURL = strings.TrimSpace(entry.DestinationURL)
		if entry.DestinationURL == "" {
			http.Error(w, "missing url", http.StatusBadRequest)
			return
		}
		if _, err := url.Parse(entry.DestinationURL); err != nil {
			http.Error(w, "invalid gateway url", http.StatusBadRequest)
			return
		}

	}
	// Generate a readable, positive name and reserve it in Redis using NX to avoid collisions.
	const maxAttempts = 10
	for i := range maxAttempts {
		entry.ID = generateGatewayID()
		ok, err := s.rdb.SetNX(ctx, entry.ID, entry.DestinationURL, s.defaultTTL).Result()
		if err != nil {
			http.Error(w, fmt.Sprintf("redis error: %v", err), http.StatusInternalServerError)
			return
		}
		if ok {
			if authorized {
				userKey := fmt.Sprintf("user:%s:gateways", userID)
				if err := s.rdb.RPush(ctx, userKey, entry.ID).Err(); err != nil {
					// Cleanup the entry if we can't store the user mapping
					_ = s.rdb.Del(ctx, entry.ID).Err()
					http.Error(w, fmt.Sprintf("redis error: %v", err), http.StatusInternalServerError)
					return
				}
				// Set TTL on the user's gateway set to match the entry TTL
				_ = s.rdb.Expire(ctx, userKey, s.defaultTTL).Err()
			}
			break
		}
		if i == maxAttempts-1 {
			http.Error(w, "could not allocate unique name; please retry", http.StatusServiceUnavailable)
			return
		}
	}
	// Populate the generated ID in the response
	w.Header().Set("Location", fmt.Sprintf("/api/gateway/%s", entry.ID))
	writeJSON(w, http.StatusCreated, entry)
}

func (s *server) handleListUserGateways(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "userID")
	if userID == "" {
		http.Error(w, "invalid userID", http.StatusBadRequest)
		return
	}
	ctx := r.Context()

	userKey := fmt.Sprintf("user:%s:gateways", userID)

	// Get all gateway IDs for this user (from the list)
	gatewayIDs, err := s.rdb.LRange(ctx, userKey, 0, -1).Result()
	if err != nil {
		http.Error(w, fmt.Sprintf("redis error: %v", err), http.StatusInternalServerError)
		return
	}

	// Fetch all gateway entries
	items := make([]GatewayEntry, 0, len(gatewayIDs))
	if len(gatewayIDs) > 0 {
		pipe := s.rdb.Pipeline()
		getCmds := make([]*redis.StringCmd, len(gatewayIDs))
		ttlCmds := make([]*redis.DurationCmd, len(gatewayIDs))

		for i, id := range gatewayIDs {
			getCmds[i] = pipe.Get(ctx, id)
			ttlCmds[i] = pipe.TTL(ctx, id)
		}

		if _, err := pipe.Exec(ctx); err != nil && !errors.Is(err, redis.Nil) {
			http.Error(w, fmt.Sprintf("redis pipeline error: %v", err), http.StatusInternalServerError)
			return
		}

		now := time.Now().UTC()
		for i, id := range gatewayIDs {
			val, vErr := getCmds[i].Result()
			if errors.Is(vErr, redis.Nil) || vErr != nil {
				continue
			}
			var expPtr time.Time
			if ttl, tErr := ttlCmds[i].Result(); tErr == nil && ttl > 0 {
				t := now.Add(ttl)
				expPtr = t
			}
			items = append(items, GatewayEntry{ID: id, DestinationURL: val, ExpiryDate: expPtr})
		}
	}

	resp := struct {
		UserID int            `json:"userID"`
		Items  []GatewayEntry `json:"items"`
		Count  int            `json:"count"`
	}{
		UserID: len(items),
		Items:  items,
		Count:  len(items),
	}
	writeJSON(w, http.StatusOK, resp)
}

// handleSnapshot returns a paginated snapshot of current Redis keys/values managed
// by this service. Pagination uses Redis SCAN cursor semantics.
//
// Request: GET /api/gateway/snapshot?cursor=<uint64>&count=<int>
// - cursor: starting scan cursor (default 0)
// - count: hint of items to return (default 100, max 1000)
//
// Response JSON:
//
//	{
//	  "items": [ {"id":"...","url":"...","expiryDate":"..."}, ... ],
//	  "nextCursor": "<uint64>",
//	  "count": <int>
//	}
func (s *server) handleSnapshot(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	// Parse cursor
	cursorStr := strings.TrimSpace(r.URL.Query().Get("cursor"))
	var cursor uint64
	if cursorStr != "" {
		if c, err := strconv.ParseUint(cursorStr, 10, 64); err == nil {
			cursor = c
		} else {
			http.Error(w, "invalid cursor", http.StatusBadRequest)
			return
		}
	}
	// Parse count
	count := 100
	if cStr := strings.TrimSpace(r.URL.Query().Get("count")); cStr != "" {
		if cVal, err := strconv.Atoi(cStr); err == nil {
			if cVal <= 0 {
				cVal = 1
			}
			if cVal > 1000 {
				cVal = 1000
			}
			count = cVal
		} else {
			http.Error(w, "invalid count", http.StatusBadRequest)
			return
		}
	}

	// Use SCAN for pagination. No pattern since keys are generated slugs without prefix.
	keys, newCursor, err := s.rdb.Scan(ctx, cursor, "", int64(count)).Result()
	if err != nil {
		http.Error(w, fmt.Sprintf("redis scan error: %v", err), http.StatusInternalServerError)
		return
	}

	// Fetch values and TTLs using pipeline for efficiency
	items := make([]GatewayEntry, 0, len(keys))
	if len(keys) > 0 {
		pipe := s.rdb.Pipeline()
		getCmds := make([]*redis.StringCmd, len(keys))
		ttlCmds := make([]*redis.DurationCmd, len(keys))
		for i, k := range keys {
			getCmds[i] = pipe.Get(ctx, k)
			ttlCmds[i] = pipe.TTL(ctx, k)
		}
		if _, err := pipe.Exec(ctx); err != nil {
			// Note: individual commands may still have results; we'll check per-cmd errors below.
			// If it's a pipeline-wide error unrelated to redis.Nil, surface it.
			if !errors.Is(err, redis.Nil) {
				http.Error(w, fmt.Sprintf("redis pipeline error: %v", err), http.StatusInternalServerError)
				return
			}
		}
		now := time.Now().UTC()
		for i, k := range keys {
			val, vErr := getCmds[i].Result()
			if errors.Is(vErr, redis.Nil) {
				// Key disappeared between SCAN and GET; skip.
				continue
			}
			if vErr != nil {
				// Skip this key but continue others.
				continue
			}
			var expPtr time.Time
			if ttl, tErr := ttlCmds[i].Result(); tErr == nil && ttl > 0 {
				t := now.Add(ttl)
				expPtr = t
			}
			items = append(items, GatewayEntry{ID: k, DestinationURL: val, ExpiryDate: expPtr})
		}
	}

	resp := struct {
		Items      []GatewayEntry `json:"items"`
		NextCursor string         `json:"nextCursor"`
		Count      int            `json:"count"`
	}{
		Items:      items,
		NextCursor: fmt.Sprintf("%d", newCursor),
		Count:      len(items),
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) routes() chi.Router {
	r := chi.NewRouter()

	// POST /api/gateways - Create new gateway
	r.Post("/api/gateways", s.handleCreateGateway)

	// GET /api/gateways/{id} - Get gateway by ID
	r.Get("/api/gateways/{id}", s.handleGetGateway)

	// GET /api/gateways/snapshot - List all gateways (pagination)
	r.Get("/api/gateways/snapshot", s.handleSnapshot)

	// GET /api/users/{userID}/gateways - List gateways for user
	r.Get("/api/users/{userID}/gateways", s.handleListUserGateways)

	// Health check
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json") //nolint:errcheck
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
