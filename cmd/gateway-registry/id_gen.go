package main

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"time"
)

// generateGatewayID creates a short, readable, positive name like
// "brave-hummingbird-42" using two positive adjectives/nouns and a number suffix.
// The output is DestinationURL-safe (lowercase, hyphen-separated).
func generateGatewayID() string {
	adj := []string{
		"brave", "bright", "calm", "cheerful", "clever", "daring", "eager", "faithful", "gentle", "glad",
		"graceful", "heroic", "honest", "hopeful", "jolly", "kind", "lively", "loyal", "merry", "noble",
		"optimistic", "patient", "peaceful", "plucky", "proud", "quick", "radiant", "shiny", "smiling", "sparkling",
		"sturdy", "sunny", "swift", "true", "vibrant", "victorious", "warm", "wise", "witty", "zesty",
	}
	noun := []string{
		"sunrise", "meadow", "river", "breeze", "forest", "harbor", "lighthouse", "hummingbird", "orchid", "starlight",
		"comet", "aurora", "ocean", "rainbow", "pebble", "summit", "prairie", "willow", "sparrow", "lantern",
		"dawn", "ember", "garden", "harvest", "horizon", "island", "journey", "maple", "meadowlark", "miracle",
		"moonbeam", "mountain", "paradise", "pathway", "phoenix", "pioneer", "serenity", "shelter", "victory", "voyage",
	}
	a := mustCryptoRandInt(len(adj))
	n := mustCryptoRandInt(len(noun))
	num := mustCryptoRandInt(1000) // 0-999
	return fmt.Sprintf("%s-%s-%d", adj[a], noun[n], num)
}

func mustCryptoRandInt(max int) int {
	if max <= 0 {
		return 0
	}
	bi := big.NewInt(int64(max))
	r, err := rand.Int(rand.Reader, bi)
	if err != nil {
		// Fallback: very unlikely, but ensure we return a value in range
		return int(time.Now().UnixNano() % int64(max))
	}
	return int(r.Int64())
}
