# bdxpolls

Private, publicly verifiable polls on [Beldex](https://beldex.io) Privacy Tokens.

- **A ballot is a token.** The organiser registers a Privacy Token (e.g. `VOTE`, 0 decimals) and sends one unit to each eligible voter.
- **A vote is a transfer.** Each answer is a subaddress of the organiser's wallet; voting = sending 1 ballot to it.
- **Secret ballot.** Ring signatures and confidential amounts hide which ballot went to which answer. The organiser sees the totals, not the voters.
- **Anyone can recount.** The server runs on a view-only wallet and publishes its address and view key; restoring it reproduces every tally.
- **Voting window by block height**, so late or early ballots are visibly ignored, not silently dropped.

## Flow

```
organiser                         voters                          anyone
─────────                         ──────                          ──────
bdxpolls token  (register VOTE)
bdxpolls new    (subaddress per option)
bdxpolls ballots ──1 VOTE each──► wallet
                                  send 1 VOTE ──► "Yes" subaddress
bdxpolls serve (view-only) ◄──────────────────────────────────── restore view-only wallet,
   tally per option, ignored, pending                            recount
```

## Commands

```
bdxpolls token   -ticker VOTE -name "Community ballots" -supply 1000000      # full wallet
bdxpolls new     -id audit-q4 -q "Fund the bridge audit?" -options "Yes,No" -token <token_id> -closes-in 2880
bdxpolls ballots -id audit-q4 -voters voters.txt                             # one address per line
bdxpolls serve   -addr 127.0.0.1:8081 -network testnet                       # view-only wallet is enough
```

Requires a `beldex-wallet-rpc` built from the `feature-privacy-token` branch (testnet, HF22).

## Status and known limitations

MVP, testnet only.
- **Ballots are transferable**, so a voter could sell theirs. Mitigation ideas: short voting windows, per-poll tokens, or a future non-transferable token flag.
- Registering a token costs a fee plus a ~180-day bond (1,000 + 10,000 BDX at the time of writing). One token can serve many polls.
- The organiser chooses the voter list; eligibility is out of scope.
- Tally integrity relies on the token's on-chain `current_supply`: counted ballots can never exceed what was minted.

Built on [`beldex-go`](https://github.com/rebrudiy/beldex-go).
