package main

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rebrudiy/beldex-go/walletrpc"
)

func testPoll() Poll {
	return Poll{
		ID: "audit", Question: "Fund the audit?", TokenID: "vote", Ticker: "VOTE", Ballots: 10,
		OpensAt: 100, ClosesAt: 200, CreatedAt: time.Now(),
		Options: []Option{{Label: "Yes", Address: "bxYes", SubIndex: 1}, {Label: "No", Address: "bxNo", SubIndex: 2}},
	}
}

func vote(txid string, sub uint32, height uint64) walletrpc.Transfer {
	return walletrpc.Transfer{TxID: txid, Amount: 1, Type: "in", TokenID: "vote", Height: height, SubaddrIndex: walletrpc.SubaddressIndex{Minor: sub}}
}

func TestCount(t *testing.T) {
	foreign := vote("f", 1, 150)
	foreign.TokenID = "other"
	pending := vote("p", 2, 0)
	pending.Type = "pool"
	double := vote("d", 1, 150)
	double.DoubleSpend = true

	got := count(testPoll(), 150, []walletrpc.Transfer{
		vote("a", 1, 120), vote("b", 1, 130), vote("c", 2, 140),
		vote("a", 1, 120),                          // listed twice
		vote("early", 1, 99), vote("late", 2, 201), // outside the window
		foreign, pending, double,
		vote("x", 9, 150), // not an option of this poll
	})

	if got.Results[0].Votes != 2 || got.Results[1].Votes != 1 {
		t.Errorf("votes = %d/%d, want 2/1", got.Results[0].Votes, got.Results[1].Votes)
	}
	if got.Counted != 3 || got.Ignored != 3 {
		t.Errorf("counted=%d ignored=%d, want 3 and 3", got.Counted, got.Ignored)
	}
	if got.Results[1].Pending != 1 {
		t.Errorf("pending = %d, want 1", got.Results[1].Pending)
	}
	if !got.Open {
		t.Error("poll should be open at height 150")
	}
	if count(testPoll(), 201, nil).Open {
		t.Error("poll should be closed at height 201")
	}
}

type fakeWallet struct{ transfers []walletrpc.Transfer }

func (f *fakeWallet) Refresh(context.Context) error                  { return nil }
func (f *fakeWallet) Height(context.Context) (uint64, error)         { return 150, nil }
func (f *fakeWallet) PrimaryAddress(context.Context) (string, error) { return "bxPrimary", nil }
func (f *fakeWallet) ViewKey(context.Context) (string, error)        { return "vk", nil }
func (f *fakeWallet) Incoming(context.Context, ...uint32) ([]walletrpc.Transfer, error) {
	return f.transfers, nil
}

func TestPollPage(t *testing.T) {
	store, err := OpenStore(filepath.Join(t.TempDir(), "p.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(testPoll()); err != nil {
		t.Fatal(err)
	}
	tl := NewTallier(&fakeWallet{transfers: []walletrpc.Transfer{vote("a", 1, 120), vote("b", 2, 130), vote("c", 1, 140)}}, store)
	if err := tl.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	srv := &Server{store: store, tallier: tl, audit: Audit{PrimaryAddress: "bxPrimary", ViewKey: "vk", Network: "testnet"}}
	rec := httptest.NewRecorder()
	srv.Routes().ServeHTTP(rec, httptest.NewRequest("GET", "/p/audit", nil))
	body := rec.Body.String()
	for _, want := range []string{"Fund the audit?", "<b>Yes</b> — 2", "<b>No</b> — 1", "width:66%", "3 of 10 ballots", "bxYes", "vk"} {
		if !strings.Contains(body, want) {
			t.Errorf("page missing %q", want)
		}
	}
}
