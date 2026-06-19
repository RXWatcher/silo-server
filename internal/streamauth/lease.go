// Package streamauth implements the session-deny half of TR-lease: an admin
// Stop/Terminate writes a per-session "deny" marker to Redis, and the offload
// nodes read it on the serve path and enforce it by withholding bytes. This is
// the one revocation case that the offloaded topology cannot otherwise cover —
// a node-served direct/remux stream has no producer to kill and is authorized by
// a still-valid 24h signed token, so without this marker an admin "kill this
// stream" would not take effect until the token expired.
//
// Scope is deliberately narrow. New playback is already blocked at central
// (`/playback/start` is RequireAuth), and a cooperative client is stopped over
// the realtime WebSocket. Only the explicit, session-scoped admin kill of a
// non-cooperative node-served stream needs this. Passive bans / partial access
// changes are NOT enforced here: they take effect at the next play and are
// otherwise bounded by the token TTL — see the architecture doc's residuals.
package streamauth

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// KeyPrefix namespaces per-session deny keys: silo:streamauth:<sid>.
const KeyPrefix = "silo:streamauth:"

// DefaultDenyTTL bounds how long a deny marker survives. It must be ≥ the
// stream-token lifetime: a denied session never receives a fresh token, so once
// every token minted before the deny has expired the marker can lapse
// harmlessly. It matches playback.MaxTokenTTL (24h).
const DefaultDenyTTL = 24 * time.Hour

// Store is the Redis-backed deny store shared by central (writer, on admin kill)
// and the offload nodes (reader, on every serve).
type Store struct {
	rdb *redis.Client
	ttl time.Duration
}

// NewStore wraps a Redis client. A nil client yields a disabled store whose
// reads fail open and whose writes no-op, so a single integrated box (no Redis)
// needs no special-casing.
func NewStore(rdb *redis.Client, ttl time.Duration) *Store {
	if ttl <= 0 {
		ttl = DefaultDenyTTL
	}
	return &Store{rdb: rdb, ttl: ttl}
}

func key(sessionID string) string { return KeyPrefix + sessionID }

// Deny marks a session revoked so the nodes refuse it on the next request.
func (s *Store) Deny(ctx context.Context, sessionID string) error {
	if s == nil || s.rdb == nil || sessionID == "" {
		return nil
	}
	return s.rdb.Set(ctx, key(sessionID), "deny", s.ttl).Err()
}

// Allowed reports whether a node should serve sessionID. It fails OPEN: only a
// present deny marker withholds bytes; an absent key (the normal case) or any
// Redis error serves, preferring availability over revocation latency.
func (s *Store) Allowed(ctx context.Context, sessionID string) bool {
	if s == nil || s.rdb == nil || sessionID == "" {
		return true
	}
	if _, err := s.rdb.Get(ctx, key(sessionID)).Result(); err != nil {
		return true // redis.Nil (absent) or a transport error → serve
	}
	return false // any present key is a deny
}
