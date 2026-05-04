package store

import (
	"encoding/json"
	"sync"
	"time"
)

type entry struct {
	data      json.RawMessage
	expiresAt time.Time
}

// TranscriptStore is a TTL-based in-memory store for serialised debate transcripts.
// All methods are safe for concurrent use.
type TranscriptStore struct {
	mu      sync.RWMutex
	entries map[string]entry
	ttl     time.Duration
}

// NewTranscriptStore creates a store with the given TTL and starts a background
// cleanup goroutine. Call the returned stop func to halt the goroutine.
func NewTranscriptStore(ttl time.Duration) (*TranscriptStore, func()) {
	s := &TranscriptStore{
		entries: make(map[string]entry),
		ttl:     ttl,
	}

	ticker := time.NewTicker(ttl / 2)
	stop := func() { ticker.Stop() }

	go func() {
		for range ticker.C {
			s.evict()
		}
	}()

	return s, stop
}

// Put stores a serialised transcript under id, replacing any existing entry.
func (s *TranscriptStore) Put(id string, data json.RawMessage) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[id] = entry{
		data:      data,
		expiresAt: time.Now().Add(s.ttl),
	}
}

// Get retrieves a transcript by id. Returns (nil, false) if not found or expired.
func (s *TranscriptStore) Get(id string) (json.RawMessage, bool) {
	s.mu.RLock()
	e, ok := s.entries[id]
	s.mu.RUnlock()

	if !ok || time.Now().After(e.expiresAt) {
		return nil, false
	}
	return e.data, true
}

// evict removes all expired entries.
func (s *TranscriptStore) evict() {
	now := time.Now()
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, e := range s.entries {
		if now.After(e.expiresAt) {
			delete(s.entries, id)
		}
	}
}
