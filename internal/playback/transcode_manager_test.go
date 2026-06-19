package playback

import (
	"context"
	"testing"
)

// fakeSessionRegistry is a GetSession + RegisterReconstructed double.
type fakeSessionRegistry struct {
	sessions map[string]*Session
}

func (f *fakeSessionRegistry) GetSession(id string) (*Session, error) {
	if s, ok := f.sessions[id]; ok {
		return s, nil
	}
	return nil, ErrSessionNotFound
}

func (f *fakeSessionRegistry) RegisterReconstructed(s *Session) *Session {
	if f.sessions == nil {
		f.sessions = map[string]*Session{}
	}
	if existing, ok := f.sessions[s.ID]; ok {
		return existing
	}
	f.sessions[s.ID] = s
	return s
}

// CloseTranscodeSession stops the live session and drops it from the transcode
// map. Under token-carried reconstruction there is no durable card to delete; the
// segment dir is reaped later by liveness+age cleanup.
func TestCloseTranscodeSession_DropsLiveSession(t *testing.T) {
	m := NewTranscodeManager()
	m.RegisterTranscodeSession("s1", &TranscodeSession{})
	m.CloseTranscodeSession("s1", "")
	if got := m.GetTranscodeSession("s1"); got != nil {
		t.Fatal("session must be removed from the live map on close")
	}
}

func TestLoadOrReconstructSession(t *testing.T) {
	ctx := context.Background()

	newMgr := func(reg *fakeSessionRegistry) *TranscodeManager {
		m := NewTranscodeManager()
		m.Sessions = reg
		return m
	}
	cardPtr := func(c RecipeCard) *RecipeCard { return &c }

	t.Run("live session, matching owner -> loaded", func(t *testing.T) {
		reg := &fakeSessionRegistry{sessions: map[string]*Session{"s": {ID: "s", UserID: 5}}}
		m := newMgr(reg)
		got, status := m.LoadOrReconstructSession(ctx, reg.GetSession, "s", 5, nil)
		if status != SessionLoaded || got == nil || got.ID != "s" {
			t.Fatalf("got status=%v session=%+v", status, got)
		}
	})

	t.Run("live session, mismatched owner -> forbidden", func(t *testing.T) {
		reg := &fakeSessionRegistry{sessions: map[string]*Session{"s": {ID: "s", UserID: 5}}}
		m := newMgr(reg)
		if _, status := m.LoadOrReconstructSession(ctx, reg.GetSession, "s", 9, nil); status != SessionForbidden {
			t.Fatalf("status = %v, want forbidden", status)
		}
	})

	t.Run("live session, zero caller -> loaded (UUID as bearer)", func(t *testing.T) {
		reg := &fakeSessionRegistry{sessions: map[string]*Session{"s": {ID: "s", UserID: 5}}}
		m := newMgr(reg)
		if _, status := m.LoadOrReconstructSession(ctx, reg.GetSession, "s", 0, nil); status != SessionLoaded {
			t.Fatalf("status = %v, want loaded", status)
		}
	})

	t.Run("miss + remux token + matching owner -> reconstructed with method", func(t *testing.T) {
		reg := &fakeSessionRegistry{}
		m := newMgr(reg)
		card := NewRemuxRecipeCard("s", 5, "p", 77, true, 2)
		got, status := m.LoadOrReconstructSession(ctx, reg.GetSession, "s", 5, cardPtr(card))
		if status != SessionLoaded || got == nil {
			t.Fatalf("status=%v session=%+v", status, got)
		}
		if got.PlayMethod != PlayRemux || got.MediaFileID != 77 || !got.TranscodeAudio || got.AudioTrackIndex != 2 {
			t.Fatalf("reconstructed remux session wrong: %+v", got)
		}
		if _, err := reg.GetSession("s"); err != nil {
			t.Fatalf("reconstructed session not registered: %v", err)
		}
	})

	t.Run("miss + token + mismatched owner -> missing (reconstruct refuses)", func(t *testing.T) {
		reg := &fakeSessionRegistry{}
		m := newMgr(reg)
		card := NewDirectRecipeCard("s", 5, "p", 77)
		if _, status := m.LoadOrReconstructSession(ctx, reg.GetSession, "s", 9, cardPtr(card)); status != SessionMissing {
			t.Fatalf("status = %v, want missing", status)
		}
	})

	t.Run("miss + token for a different session id -> missing", func(t *testing.T) {
		reg := &fakeSessionRegistry{}
		m := newMgr(reg)
		card := NewDirectRecipeCard("other", 5, "p", 77)
		if _, status := m.LoadOrReconstructSession(ctx, reg.GetSession, "s", 5, cardPtr(card)); status != SessionMissing {
			t.Fatalf("status = %v, want missing (card session id mismatch)", status)
		}
	})

	t.Run("miss + no token -> missing", func(t *testing.T) {
		reg := &fakeSessionRegistry{}
		m := newMgr(reg)
		if _, status := m.LoadOrReconstructSession(ctx, reg.GetSession, "nope", 5, nil); status != SessionMissing {
			t.Fatalf("status = %v, want missing", status)
		}
	})
}

// acquireReconstructSlot must bound concurrent reconstructs and let a caller
// whose request is cancelled give up its place instead of queueing dead work.
func TestAcquireReconstructSlot(t *testing.T) {
	m := &TranscodeManager{reconstructSem: make(chan struct{}, 1)}

	release, ok := m.acquireReconstructSlot(context.Background())
	if !ok {
		t.Fatal("first acquire should succeed")
	}

	// Cap is full: a cancelled request must back off rather than block forever.
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, ok := m.acquireReconstructSlot(cancelled); ok {
		t.Fatal("acquire on a full semaphore with a cancelled context must fail")
	}

	// Releasing frees the slot for the next reconstruct.
	release()
	release2, ok := m.acquireReconstructSlot(context.Background())
	if !ok {
		t.Fatal("acquire should succeed after the slot is released")
	}
	release2()
}
