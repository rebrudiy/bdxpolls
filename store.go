package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Option is one answer. Votes for it are ballot tokens sent to its subaddress.
type Option struct {
	Label    string `json:"label"`
	Address  string `json:"address"`
	SubIndex uint32 `json:"sub_index"`
}

// Poll is one question. Every poll uses the organiser's ballot token; a vote
// is exactly one token unit sent to an option's subaddress.
type Poll struct {
	ID        string    `json:"id"`
	Question  string    `json:"question"`
	TokenID   string    `json:"token_id"`
	Ticker    string    `json:"ticker"`
	Options   []Option  `json:"options"`
	Ballots   uint64    `json:"ballots"`   // ballots issued to voters for this poll
	OpensAt   uint64    `json:"opens_at"`  // block height; votes mined before it are ignored
	ClosesAt  uint64    `json:"closes_at"` // block height; votes mined after it are ignored
	CreatedAt time.Time `json:"created_at"`
}

type Store struct {
	path string
	mu   sync.RWMutex
	byID map[string]Poll
}

var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,62}$`)

func OpenStore(path string) (*Store, error) {
	s := &Store{path: path, byID: map[string]Poll{}}
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Poll
	if err := json.Unmarshal(b, &list); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for _, p := range list {
		s.byID[p.ID] = p
	}
	return s, nil
}

func (s *Store) Get(id string) (Poll, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.byID[id]
	return p, ok
}

func (s *Store) List() []Poll {
	s.mu.RLock()
	defer s.mu.RUnlock()
	list := make([]Poll, 0, len(s.byID))
	for _, p := range s.byID {
		list = append(list, p)
	}
	sort.Slice(list, func(i, j int) bool { return list[i].CreatedAt.After(list[j].CreatedAt) })
	return list
}

// Put inserts or replaces a poll.
func (s *Store) Put(p Poll) error {
	if !slugRe.MatchString(p.ID) {
		return fmt.Errorf("id %q: use 2-63 chars of a-z, 0-9 and '-'", p.ID)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.byID[p.ID] = p
	return s.saveLocked()
}

func (s *Store) saveLocked() error {
	list := make([]Poll, 0, len(s.byID))
	for _, p := range s.byID {
		list = append(list, p)
	}
	b, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o755); err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
