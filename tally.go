package main

import (
	"context"
	"log"
	"sync"
	"time"

	"github.com/rebrudiy/beldex-go/walletrpc"
)

// Wallet is what the server needs. A view-only wallet is enough: it can see
// ballots arriving at option subaddresses but cannot move them.
type Wallet interface {
	Refresh(ctx context.Context) error
	Incoming(ctx context.Context, indices ...uint32) ([]walletrpc.Transfer, error)
	Height(ctx context.Context) (uint64, error)
	PrimaryAddress(ctx context.Context) (string, error)
	ViewKey(ctx context.Context) (string, error)
}

type Result struct {
	Label   string `json:"label"`
	Address string `json:"address"`
	Votes   uint64 `json:"votes"`
	Pending uint64 `json:"pending"` // in mempool, not counted yet
}

// Tally is a poll's current count. Ignored holds ballots that arrived outside
// the voting window or in a different token, so the numbers always add up.
type Tally struct {
	Results []Result `json:"results"`
	Counted uint64   `json:"counted"`
	Ignored uint64   `json:"ignored"`
	Height  uint64   `json:"height"`
	Open    bool     `json:"open"`
}

func count(p Poll, height uint64, transfers []walletrpc.Transfer) Tally {
	t := Tally{Height: height, Open: height >= p.OpensAt && (p.ClosesAt == 0 || height <= p.ClosesAt)}
	byIndex := map[uint32]int{}
	for i, o := range p.Options {
		t.Results = append(t.Results, Result{Label: o.Label, Address: o.Address})
		byIndex[o.SubIndex] = i
	}

	type key struct {
		txid string
		sub  uint32
	}
	seen := map[key]bool{} // a tx can be listed both as "in" and "pool" during a race
	for _, tr := range transfers {
		i, isOption := byIndex[tr.SubaddrIndex.Minor]
		k := key{tr.TxID, tr.SubaddrIndex.Minor}
		if !isOption || tr.DoubleSpend || seen[k] {
			continue
		}
		seen[k] = true
		if tr.TokenID != p.TokenID {
			t.Ignored += tr.Amount // BDX or a foreign token sent to an option address
			continue
		}
		if tr.Type != "in" {
			t.Results[i].Pending += tr.Amount
			continue
		}
		if tr.Height < p.OpensAt || (p.ClosesAt != 0 && tr.Height > p.ClosesAt) {
			t.Ignored += tr.Amount
			continue
		}
		t.Results[i].Votes += tr.Amount
		t.Counted += tr.Amount
	}
	return t
}

// Tallier polls the wallet and caches tallies for all polls.
type Tallier struct {
	wallet Wallet
	store  *Store

	mu      sync.RWMutex
	byPoll  map[string]Tally
	updated time.Time
}

func NewTallier(w Wallet, s *Store) *Tallier {
	return &Tallier{wallet: w, store: s, byPoll: map[string]Tally{}}
}

func (t *Tallier) Tally(id string) (Tally, time.Time) {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.byPoll[id], t.updated
}

func (t *Tallier) Run(ctx context.Context, every time.Duration) {
	for {
		if err := t.Poll(ctx); err != nil {
			log.Printf("poll: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(every):
		}
	}
}

func (t *Tallier) Poll(ctx context.Context) error {
	polls := t.store.List()
	if len(polls) == 0 {
		return nil
	}
	if err := t.wallet.Refresh(ctx); err != nil {
		return err
	}
	height, err := t.wallet.Height(ctx)
	if err != nil {
		return err
	}
	var indices []uint32
	for _, p := range polls {
		for _, o := range p.Options {
			indices = append(indices, o.SubIndex)
		}
	}
	transfers, err := t.wallet.Incoming(ctx, indices...)
	if err != nil {
		return err
	}

	byPoll := make(map[string]Tally, len(polls))
	for _, p := range polls {
		byPoll[p.ID] = count(p, height, transfers)
	}
	t.mu.Lock()
	t.byPoll, t.updated = byPoll, time.Now()
	t.mu.Unlock()
	return nil
}
