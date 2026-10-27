// bdxpolls runs private, publicly verifiable polls on Beldex Privacy Tokens.
//
// A ballot is one unit of the organiser's token. Each option is a subaddress
// of the organiser's wallet; voting = sending one ballot to an option.
// Ring signatures hide who voted for what; the published view key lets anyone
// recount.
//
//	bdxpolls token   -ticker VOTE -name "Community ballots" -supply 1000000
//	bdxpolls new     -id treasury-q4 -q "Fund the bridge audit?" -options "Yes,No" -token <id> -closes-in 2880
//	bdxpolls ballots -id treasury-q4 -voters voters.txt
//	bdxpolls serve
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/rebrudiy/beldex-go/walletrpc"
)

// maxDestinations keeps ballot transactions comfortably under Beldex's output limit.
const maxDestinations = 15

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "usage: bdxpolls token|new|ballots|serve [flags]")
		os.Exit(2)
	}
	cmds := map[string]func([]string) error{"token": cmdToken, "new": cmdNew, "ballots": cmdBallots, "serve": cmdServe}
	cmd, ok := cmds[os.Args[1]]
	if !ok {
		log.Fatalf("unknown command %q", os.Args[1])
	}
	if err := cmd(os.Args[2:]); err != nil {
		log.Fatal(err)
	}
}

type common struct {
	wallet *string
	data   *string
}

func commonFlags(fs *flag.FlagSet) common {
	return common{
		wallet: fs.String("wallet", "http://127.0.0.1:29092", "beldex-wallet-rpc URL"),
		data:   fs.String("data", "data/polls.json", "poll store"),
	}
}

// cmdToken registers the organiser's ballot token. Needs the full (spending) wallet.
func cmdToken(args []string) error {
	fs := flag.NewFlagSet("token", flag.ExitOnError)
	c := commonFlags(fs)
	ticker := fs.String("ticker", "VOTE", "token ticker")
	name := fs.String("name", "", "token full name")
	supply := fs.Uint64("supply", 1_000_000, "max ballots ever")
	fs.Parse(args)
	if *name == "" {
		return fmt.Errorf("-name is required")
	}
	tok, err := walletrpc.New(*c.wallet).RegisterToken(context.Background(), walletrpc.TokenDescriptor{
		Ticker: *ticker, FullName: *name, TotalMaxSupply: *supply, DecimalPoint: 0,
		MetaInfo: "bdxpolls ballot: 1 unit = 1 vote",
	})
	if err != nil {
		return err
	}
	fmt.Printf("token %s (%s) registered in tx %s\n", tok.TokenID, tok.Ticker, tok.TxHash)
	return nil
}

func cmdNew(args []string) error {
	fs := flag.NewFlagSet("new", flag.ExitOnError)
	c := commonFlags(fs)
	id := fs.String("id", "", "URL slug")
	question := fs.String("q", "", "question")
	options := fs.String("options", "Yes,No", "comma-separated options")
	tokenID := fs.String("token", "", "ballot token id")
	ticker := fs.String("ticker", "VOTE", "ballot token ticker, for display")
	closesIn := fs.Uint64("closes-in", 2880, "voting window in blocks (2880 ≈ 1 day)")
	fs.Parse(args)
	if *id == "" || *question == "" || *tokenID == "" {
		return fmt.Errorf("-id, -q and -token are required")
	}
	store, err := OpenStore(*c.data)
	if err != nil {
		return err
	}
	if _, dup := store.Get(*id); dup {
		return fmt.Errorf("poll %q already exists", *id)
	}

	ctx := context.Background()
	w := walletrpc.New(*c.wallet)
	height, err := w.Height(ctx)
	if err != nil {
		return err
	}
	p := Poll{ID: *id, Question: *question, TokenID: *tokenID, Ticker: *ticker,
		OpensAt: height, ClosesAt: height + *closesIn, CreatedAt: time.Now()}
	for _, label := range strings.Split(*options, ",") {
		label = strings.TrimSpace(label)
		addr, idx, err := w.NewSubaddress(ctx, "bdxpolls:"+*id+":"+label)
		if err != nil {
			return err
		}
		p.Options = append(p.Options, Option{Label: label, Address: addr, SubIndex: idx})
	}
	if len(p.Options) < 2 {
		return fmt.Errorf("need at least 2 options")
	}
	if err := store.Put(p); err != nil {
		return err
	}
	fmt.Printf("poll %s open from block %d to %d\n", p.ID, p.OpensAt, p.ClosesAt)
	for _, o := range p.Options {
		fmt.Printf("  %-12s %s\n", o.Label, o.Address)
	}
	return nil
}

// cmdBallots mints one ballot per voter and sends them out in batches.
func cmdBallots(args []string) error {
	fs := flag.NewFlagSet("ballots", flag.ExitOnError)
	c := commonFlags(fs)
	id := fs.String("id", "", "poll id")
	votersFile := fs.String("voters", "", "file with one voter address per line")
	fs.Parse(args)
	store, err := OpenStore(*c.data)
	if err != nil {
		return err
	}
	p, ok := store.Get(*id)
	if !ok {
		return fmt.Errorf("unknown poll %q", *id)
	}
	voters, err := readLines(*votersFile)
	if err != nil {
		return err
	}
	if len(voters) == 0 {
		return fmt.Errorf("no voters in %s", *votersFile)
	}

	ctx := context.Background()
	w := walletrpc.New(*c.wallet)
	mint, err := w.MintToken(ctx, p.TokenID, uint64(len(voters)))
	if err != nil {
		return fmt.Errorf("mint: %w", err)
	}
	fmt.Printf("minted %d ballots in %s; wait for it to unlock before sending\n", len(voters), mint.TxHash)

	for start := 0; start < len(voters); start += maxDestinations {
		batch := voters[start:min(start+maxDestinations, len(voters))]
		dests := make([]walletrpc.Destination, len(batch))
		for i, addr := range batch {
			dests[i] = walletrpc.Destination{Address: addr, Amount: 1}
		}
		tx, err := w.SendToken(ctx, p.TokenID, dests...)
		if err != nil {
			return fmt.Errorf("sending ballots %d-%d: %w", start, start+len(batch), err)
		}
		fmt.Printf("sent %d ballots in %s\n", len(batch), tx.TxHash)
	}
	p.Ballots += uint64(len(voters))
	return store.Put(p)
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	c := commonFlags(fs)
	addr := fs.String("addr", "127.0.0.1:8081", "listen address")
	network := fs.String("network", "testnet", "mainnet|testnet, shown in audit instructions")
	every := fs.Duration("poll", 15*time.Second, "wallet poll interval")
	fs.Parse(args)

	store, err := OpenStore(*c.data)
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	w := walletrpc.New(*c.wallet)
	primary, err := w.PrimaryAddress(ctx)
	if err != nil {
		return fmt.Errorf("wallet not reachable: %w", err)
	}
	viewKey, err := w.ViewKey(ctx)
	if err != nil {
		return err
	}
	tallier := NewTallier(w, store)
	go tallier.Run(ctx, *every)

	srv := &Server{store: store, tallier: tallier, audit: Audit{PrimaryAddress: primary, ViewKey: viewKey, Network: *network}}
	httpSrv := &http.Server{Addr: *addr, Handler: srv.Routes(), ReadHeaderTimeout: 5 * time.Second}
	go func() {
		<-ctx.Done()
		httpSrv.Shutdown(context.Background())
	}()
	log.Printf("bdxpolls on http://%s (%d polls)", *addr, len(store.List()))
	if err := httpSrv.ListenAndServe(); err != http.ErrServerClosed {
		return err
	}
	return nil
}

func readLines(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var lines []string
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if line := strings.TrimSpace(sc.Text()); line != "" && !strings.HasPrefix(line, "#") {
			lines = append(lines, line)
		}
	}
	return lines, sc.Err()
}
