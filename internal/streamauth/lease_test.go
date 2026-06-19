package streamauth

import (
	"context"
	"testing"
)

// Without Redis (a single integrated box), the deny store must fail open so it
// never blocks playback, and writes must no-op rather than error.
func TestStoreFailsOpenWithoutRedis(t *testing.T) {
	ctx := context.Background()

	var nilStore *Store // nil receiver
	if !nilStore.Allowed(ctx, "x") {
		t.Fatal("nil store must fail open")
	}
	if err := nilStore.Deny(ctx, "x"); err != nil {
		t.Fatalf("nil store Deny should no-op, got %v", err)
	}

	s := NewStore(nil, 0)
	if !s.Allowed(ctx, "x") {
		t.Fatal("store over a nil client must fail open")
	}
	if err := s.Deny(ctx, "x"); err != nil {
		t.Fatalf("disabled Deny should no-op, got %v", err)
	}
	if s.ttl != DefaultDenyTTL {
		t.Fatalf("ttl = %v, want default %v", s.ttl, DefaultDenyTTL)
	}
}
