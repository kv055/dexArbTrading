# Bisq post-take trade flow: investigation, gaps, and plan

This document captures the full conversation and the resulting plan for the
Bisq take → wait-for-fiat → release path. **The plan has not been implemented.**

---

## 1. Original request

Investigate the codebase after the already-ready steps (offer selection,
pre-trade checks, LND pool → Bisq wallet transfer, pool refill). Focus on what
comes after funding:

1. Take an offer
2. Wait for fiat to arrive
3. Release the offer

Question: is the codebase ready for those steps, or are parts still missing?

---

## 2. Investigation findings

### Architecture

The Bisq integration spans four services:

| Component | Role |
|---|---|
| `dexMmController` | Orchestrator: pre-trade checks, LND pool → Bisq wallet funding, offer taking, release worker |
| `dexFiatController` | Scrapes fiat payment notifications (email/push), matches them to trades, triggers release webhook |
| `dexUnifiedApi` | Fetches Bisq v1 offers via gRPC snapshotter, routes/resolves trades |
| `dexMonitor` | Dormant Bisq closed-trades stats reader (`internal/bisqstats/`) |

### Key files

**Bisq v1 gRPC client (dynamic protobuf, complete):**
- `dexMmController/internal/exchangeconnectors/dex/bisq/client.go` — full client incl. hand-built file descriptor (lines 749–951): `TakeOffer` (:340), `GetPosition` (:397), `GetOpenPositions` (:379), `ConfirmPaymentReceived` (:423), `GetCounterpartyPayment` (:439), `GetFundingAddresses` (:515), `FundingAddress` (:532), payment-account CRUD (:202–337)

**Bisq 2 connector:** `dexMmController/internal/exchangeconnectors/dex/bisq2/client.go` — **chat transport only**. No offer/take/trade APIs.

**Orchestration:** `dexMmController/internal/tradeflow/{tradeflow,hedgeless,persist,recovery}.go`; taker/releaser in `dexMmController/internal/dexaccount/{taker,releaser,payments,direct_resolver}.go`

### Step-by-step assessment

#### 1. Offer selection — implemented

- `dexUnifiedApi/cmd/snapshot/main.go:301-333` — fetches Bisq v1 offers over gRPC (`stub.GetOffers`). Uses patched `offerinfo-witness.patch` for account-age witness data.
- `dexMmController/internal/premiumoffers/watcher.go:308` (`bestOffer` — highest net-premium) and `:355` (`takeBestOffer`) auto-select and record candidates; also manual UI selection flows into `tradeflow.Order.OfferID` (`tradeflow/tradeflow.go:64`).

#### 2. Pre-trade checks — implemented

- `dexMmController/internal/pretrade/checker.go` — `BidChecker` (858 lines): CEX balance, whitelist, API permissions, payment setup/chat templates, LND/Lightning pool liquidity, LSP/swap-quote checks.
- Invoked in hedgefull path at `tradeflow/tradeflow.go:345` (gating at `preTradePassed` :519, with `onchain_direct` whitelist exemption :525) and hedgeless path at `tradeflow/hedgeless.go:123-139`.

#### 3. BTC from LND pool/wallet to Bisq wallet — implemented

- Hedgeless route: `tradeflow/hedgeless.go:222-256` (`fundHedgelessDEX`, bisq branch) — explicit ordering "LND send → observe Bisq balance → take offer": `tf.hedgelessWallet.SendOnChain(ctx, address, amountSat, ...)` at :241, then `waitForBisqFunding` (:362-382, polls `DirectBalanceBTC` vs baseline+amount, 10 min timeout).
- Address/balance source: `dexMmController/internal/dexaccount/direct_resolver.go:26` (`DirectDestination` → `bisq.FundingAddress`) and `:41` (`DirectBalanceBTC` → `GetFundingAddresses`).
- Hedgefull direct route: CEX withdrawal targets the Bisq wallet directly (`tradeflow/tradeflow.go:536-549`, `resolveWithdrawTarget`).

#### 4. Pool refill — implemented (hedgeless mode)

- Full lifecycle state machine `pending → spot_buy_pending → spot_bought → withdrawal_initiated → restored | needs_review` (`hedgeless.go:31-43`).
- `startRefill` :384 (spot buy + withdrawal back to LND), background worker `RunRefillWorker` :551 / `advanceRefill` :586 (crash recovery, idempotency by stable client IDs, max 2 attempts), operator reconciliation `ReconcilePoolRefill` :726. Triggered immediately after pool spend via the `onPoolSpent` callback :175-194.
- N/A for hedgefull mode (CEX buys first, then withdraws).

#### 5. Taking a Bisq offer — implemented

- Client: `exchangeconnectors/dex/bisq/client.go:340` (`TakeOffer`, gRPC `Trades/TakeOffer`, dynamic proto `TakeOfferRequest` :846).
- Venue dispatch: `dexaccount/taker.go:156` (`takeBisq`), called from `tradeflow/hedgeless.go:252` after wallet-funding confirmation. Result persisted as `DexPositionOpen` (`recordHedgelessDEXOpen` :486).

#### 6. Waiting for fiat payment — partial (out-of-band, not via Bisq trade state)

- There is **no code that polls Bisq trade phases** (nothing matches `FIAT_SENT` / `PAYMENT_RECEIVED` anywhere). The `Trade.Phase` field is parsed (`bisq/client.go:686`) but never used for fiat-wait logic.
- Instead, fiat arrival is detected externally by **dexFiatController** scraping bank/payment-provider notifications: `dexFiatController/internal/paymentevents/processor.go` (match + channel correlation + release trigger :100-117), `matcher.go`, `events.go:362` (atomic `DexPositionTaken → DexFiatHasArrived` advancement), ambiguous matches escalated to Telegram approval (`confirmation.go`).
- On the controller side, `healthcheck/healthcheck.go:340-370` (`verifyDexPositionTaken`) can advance fiat-arrived **only for HodlHodl** (`ContractIsPaid` :354); for Bisq it runs only `checkBisqAnomaly` (:313-336 — failure/error-message alerting via `GetPosition`), i.e., no Bisq-native fiat-sent detection fallback.

#### 7. Release / completing the offer — implemented

- Client: `bisq/client.go:423` (`ConfirmPaymentReceived` — daemon signs/broadcasts multisig payout; payout txid read back via `GetPosition`).
- Venue dispatch: `dexaccount/releaser.go:143` (`releaseBisq`, returns payout tx id, state, phase).
- Stage-6 worker: `dexrelease/worker.go:104` (`release` — idempotent, requires status `DexFiatHasArrived`, sets `StatusDexPositionReleased`), fed by authenticated webhook `api/dex_release.go:37` (`handleDexReleaseWebhook`), which dexFiatController calls via `pushpush/release.go`.
- Counterparty payment details for the fiat payment come from the structured contract: `payments.go:102` (`FetchCounterparty` → `fetchBisq` :113) using `GetCounterpartyPayment`.

### Known TODOs / stubs

| Location | Issue |
|---|---|
| `healthcheck/healthcheck.go:391` | `// TODO: implement Bisq / RoboSats release confirmation` — post-release verification against Bisq daemon missing (HodlHodl only) |
| `healthcheck/healthcheck.go:365` | RoboSats contract status check unimplemented (not Bisq, but same function) |
| `dexaccount/taker.go:222-233` | `pollBisqDeposit` is an effective stub: fetches the trade, discards it (`_ = trade`), always returns empty — harmless since Bisq deposits are pre-funded, but dead code |
| `tradeflow/tradeflow.go:630-632` | Ask-side cycle returns "not implemented yet" (bid-only system) |
| `dexaccount/payments.go:253` | Bisq1 chat transport intentionally unavailable ("daemon API exposes no trade-chat transport") |
| `dexMonitor/internal/bisqstats/reader.go:9` | Parser "intentionally left as dormant" helper |

### Bottom line of the investigation

Steps **1, 2, 3, 4, 5, and 7 are fully implemented** end-to-end for Bisq v1, with crash-safe persistence and recovery. Step **6 (waiting for fiat)** is implemented but through side-channel fiat-notification scraping (dexFiatController) rather than Bisq trade-phase monitoring — there is no Bisq-native `FIAT_SENT` / fiat-received polling fallback, and post-release Bisq confirmation is an explicit TODO.

The happy path is wired end-to-end and crash-safe. The main missing pieces:

1. No Bisq-native fiat-wait fallback — if email/push scraping misses the notification, the trade stalls (no polling safety net).
2. Post-release verification against the Bisq daemon is an explicit TODO (`healthcheck/healthcheck.go:391`).
3. Dead code: `pollBisqDeposit` in `taker.go:222-233` always returns empty.

---

## 3. Request for a detailed plan

After the investigation, the request was: make a detailed plan that would fix the gaps.

Follow-up decisions from the conversation:

- Implement **only Gap 1** (Bisq-native fiat-wait fallback).
- **No auto-advance** of transaction status. Manual Telegram confirm only.
- Gaps 2 (post-release verification) and 3 (`pollBisqDeposit` cleanup) are out of scope for this first implementation.
- RoboSats TODOs are out of scope.

A later message asked for a more detailed explanation of Gap 1 before building. The final instruction was: write the entire chat history, including the plan, into an `.md` file and **do not implement the plan yet**.

---

## 4. Plan to close the gaps (full, as originally proposed)

### Gap 1 — Bisq-native fiat-wait fallback (safety net for missed notifications)

**Context:** Today `StatusDexPositionTaken` rows for Bisq only get anomaly checks (`healthcheck.go:362-363`); fiat arrival relies solely on dexFiatController scraping. If a notification is missed, the trade stalls silently.

**Where:** `verifyDexPositionTaken`, case `db.DexBisq` (`dexMmController/internal/healthcheck/healthcheck.go:362`) — this already runs every tick (~30s) via `RunPeriodic`.

**Changes (original plan):**

1. Extend the Bisq branch of `verifyDexPositionTaken` to call `h.bisq.GetPosition(ctx, tradeID)` and interpret the parsed `Trade.Phase` / `Trade.State` (`bisq/client.go:95-97, :686`; enum values in `dexUnifiedApi/bisq_grpc/pb.proto:1671-1697`):
   - `Phase == "FIAT_SENT"` (or `State >= BUYER_SENT_FIAT_PAYMENT_INITIATED_MSG`): the counterparty claims they sent fiat. **Do not auto-release.** Instead, if the row has stayed in this state longer than a configurable delay (new env var, e.g. `BISQ_FIAT_SENT_ESCALATION_AFTER`, default ~2h), fire an escalation through the existing `notifyAnomaly` / Telegram path prompting manual confirmation.
   - Optional stronger signal: `State == SELLER_RECEIVED_FIAT_PAYMENT_INITIATED_MSG` means the buyer's "fiat sent" peer message actually arrived at our daemon — candidate for optional auto-advance. **Rejected:** escalate-only, no auto-advance.
   - Failure/dispute detection: extend `checkBisqAnomaly` (:313) to also inspect `DisputeState` from the proto, not just `HasFailed` / `ErrorMessage`. **Later scoped out of the first implementation** (the minimal hand-built descriptor does not declare dispute fields).
2. Add a small phase/state helper (e.g. `bisq.TradePhaseRank` or comparison funcs) in the bisq connector so ordering comparisons aren't string-magicked in healthcheck. Unit tests against synthetic dynamic-proto responses (pattern already used in the client's tests).

### Gap 2 — Post-release verification against the Bisq daemon (not in first implementation)

**Where:** `verifyDexPositionReleased`, case `db.DexBisq` (`healthcheck.go:390-391`, the explicit TODO).

**Changes:**

1. Implement the Bisq branch: `GetPosition(tradeID)` → consider release verified when `Phase` ∈ {`PAYOUT_PUBLISHED`, `WITHDRAWN`} **and** `trade.PayoutTxID != ""`. Then set `tx.Status = db.StatusDexPositionReleased` is already set by dexrelease worker — here mark `tx.TransactionComplete = true` + `UpdateTransaction`, mirroring the HodlHodl branch (:385-389).
2. Optional hardening (recommended): additionally verify the payout txid is visible on-chain using the already-injected `h.btc` (`bitcoin.NetworkDataFetcher`) before marking complete; if not yet seen, retry next tick.
3. On failure/dispute states instead → `notifyAnomaly`.
4. Update `healthcheck/README.md` status table (the doc currently marks these as TODO).

### Gap 3 — Dead code cleanup (not in first implementation)

**Where:** `pollBisqDeposit` (`dexMmController/internal/dexaccount/taker.go:222-233`) fetches the trade then discards it.

**Options:** either delete it and its dispatch entry (Bisq deposits are pre-funded, so it serves no purpose), or implement it to return the trade's deposit tx data for persistence into `TakeResult.Execution`. Deletion is the recommended option.

### Testing & verification (original plan)

- Unit tests per module following existing patterns: healthcheck tests for the three new Bisq branches (phase escalation, release completion, dispute alerting), connector tests for the phase helper.
- Run `go build ./... && go test ./...` in `dexMmController`.
- Docs: update `healthcheck/README.md` and `docs/internal/exchanges/dex/bisq/client.md` if signatures change (a docs-sync pass fits the repo's skill).

### Open questions from the original plan (resolved)

1. **Fiat-wait fallback policy:** escalate-to-manual-Telegram only (safe), or also auto-advance to `DexFiatHasArrived` when the daemon reports `SELLER_RECEIVED_FIAT_..._MSG`? **Decision: escalate-only, no auto-advance.**
2. **Payout verification bar:** daemon phase sufficient, or require on-chain visibility of the payout tx via the BTC watcher before marking complete? **Deferred with Gap 2.**
3. **Scope:** fix the RoboSats TODOs too, or Bisq-only? **Bisq-only, Gap 1 only.**
4. **Cleanup:** delete `pollBisqDeposit` or flesh it out? **Deferred with Gap 3.**

---

## 5. Detailed Gap 1 implementation design (approved, not yet built)

### Goal

When a Bisq counterparty marks their fiat payment as sent, the daemon knows
about it — but today our code ignores that signal entirely
(`healthcheck.go:362-363` only runs anomaly checks). If dexFiatController's
email/push scraping misses or fails to match the bank notification, the trade
sits in `StatusDexPositionTaken` forever with zero visibility. We add a
periodic daemon-poll that detects "fiat marked sent but no fiat arrival
recorded" and escalates to the operator via Telegram.

**No auto-advance:** the row's status is never touched by this code path;
release still happens exclusively through the existing
`DexFiatHasArrived` → webhook → `dexrelease` chain.

### Layer 1 — bisq connector (`internal/exchangeconnectors/dex/bisq/`)

**Why this layer needs changes at all:** the client talks to the daemon
through a *hand-built protobuf descriptor* (`buildFileDescriptor()`,
`client.go:749`). Dynamic protobuf only decodes fields the descriptor
declares — so even though the real daemon sends them, our parsed `Trade`
struct currently drops the most useful signals. The gRPC `TradeInfo`
message (`grpc.proto:613`) has exactly three boolean convenience fields
that are perfect for us:

| Field # | Name | Meaning |
|---|---|---|
| 20 | `is_payment_started_message_sent` | buyer pressed "payment started" — fiat sent |
| 21 | `is_payment_received_message_sent` | seller confirmed receipt (= post-release, out of scope here) |
| 22 | `is_payout_published` | payout broadcast (useful later for Gap 2) |

**Changes:**

1. **Descriptor** (`client.go`, inside `TradeInfo` message definition ~line 852): add three `TYPE_BOOL` fields for numbers 20, 21, 22. Field numbers must match the daemon's wire format exactly — they do per `grpc.proto`.
2. **`Trade` struct** (~line 87): add `IsPaymentStartedMessageSent`, `IsPaymentReceivedMessageSent`, `IsPayoutPublished bool`.
3. **Parsing** (`tradeFromMessage`, `client.go:681`): read the three bools via the existing `boolField` helper.
4. **New file `tradephase.go`** with a `FiatSent()` method on `*Trade` implementing a triple-redundant check:
   - primary: `IsPaymentStartedMessageSent == true` (explicit boolean from the daemon, immune to enum naming changes);
   - fallback A: `Phase >= FIAT_SENT` using a rank table mirroring the Phase enum (`INIT=1 … WITHDRAWN=8`, `pb.proto:1688`);
   - fallback B: `State >= BUYER_SENT_FIAT_PAYMENT_INITIATED_MSG` using a rank table mirroring the State enum where that value = 17 (`pb.proto:1672`).

Redundancy matters because older daemons populate the string enums reliably
while the booleans were added later, and vice versa. Unknown phase/state
names return "not ranked" and are skipped rather than guessed. Keeping the
rank tables unexported in the connector means healthcheck never does string
comparisons against Bisq internals.

### Layer 2 — healthcheck (`internal/healthcheck/healthcheck.go`)

**Current shape:** `verifyDexPositionTaken` case `db.DexBisq` calls
`checkBisqAnomaly` which fetches the trade itself (:321). We'd otherwise hit
`GetPosition` twice per tick, so:

1. **Refactor into one fetch + two checks.** New method `verifyBisqPositionTaken(ctx, tx)`:
   - resolve tradeID via existing `dexContractID(tx)` (returns nil quietly if absent — preserves current semantics);
   - one `GetPosition` call;
   - run the existing failure/error-message anomaly logic on the fetched trade (extracted from `checkBisqAnomaly` so the function keeps its name/behavior for other callers — actually it has no other callers, so it becomes fetch-free and takes the trade as an argument);
   - then run the new escalation check below.

2. **Escalation condition — all four must hold:**
   - `h.bisq != nil && h.anomaly != nil` (feature silently off without wiring — same convention as `checkBisqAnomaly`);
   - `trade.FiatSent()` true;
   - elapsed time since `tx.UpdatedAt` ≥ configured delay;
   - this tradeID hasn't been escalated yet.

   **Why `tx.UpdatedAt`:** once a row reaches `StatusDexPositionTaken` nothing writes to it until status advances (verified: `captureChatPayment` runs earlier, `checkBisqAnomaly` never writes), so `UpdatedAt` ≈ take time. Measuring the delay from take-time rather than "first time we observed FIAT_SENT" is deliberately simpler: it needs no schema change, no new jsonb envelope, survives process restarts, and is strictly more conservative (the timer starts earlier). The cost is that slow-taking buyers get escalated somewhat later than strictly necessary — acceptable for a safety net whose purpose is "nothing else fired".

   **Why in-memory dedupe:** the `AnomalyNotifier` (`anomaly.go:45`) sends unconditionally — every 30s tick would spam Telegram. A `map[string]struct{}` of escalated trade IDs guarded by a `sync.Mutex` on the `HealthChecker` singleton (created once in `main.go:420`, so lifetime matches the process) fixes that. Restart resets the map → worst case **one duplicate alert per stuck trade after the delay elapses again**, which is arguably correct behavior after an operator restart anyway. No DB migration needed, which matters given the repo's strict forward-only migration rules.

   **Notification text** goes through the existing `notifyAnomaly` → `AnomalyNotifier.NotifyAnomaly` → Telegram pipe, e.g.: reason `"counterparty marked fiat sent — no bank notification matched"`, detail carrying phase, state, fiat amount/currency, and hours since take, so the operator can act without opening anything else. The operator confirms manually via whatever channel they use today; we deliberately don't build a confirm button here since the release worker already gates on `DexFiatHasArrived`.

3. **Config:** new field `bisqFiatEscalationDelay time.Duration` + builder `WithBisqFiatEscalationDelay(d)`. Semantics: `d <= 0` disables escalation entirely; default constant `2h`. Wired in `main.go` next to the other health knobs: `parseAccountHealthInterval(envOr("BISQ_FIAT_ESCALATION_AFTER", "2h"))`.

### Layer 3 — wiring & docs

- `main.go:420-424`: append `.WithBisqFiatEscalationDelay(...)` to the existing builder chain.
- `healthcheck/README.md`: update the status-table row for `DexPositionTaken` (currently says Bisq = anomaly-only) and document the new env var.
- `docs/internal/exchanges/dex/bisq/client.md`: add the three new `Trade` fields / `FiatSent()` signature (repo convention per the docs-sync skill).

### Explicitly out of scope for this first implementation

- No writes to `tx.Status` anywhere in this feature — the row stays `StatusDexPositionTaken` until dexFiatController advances it or the operator intervenes.
- No dispute-state detection (the minimal descriptor doesn't declare dispute fields; adding them is its own change).
- RoboSats TODOs and Gaps 2/3 untouched.

### Tests

1. `bisq` package: build synthetic dynamicpb `GetTradeReply` messages via the package's own `newMessage` / `setBool` / `setString` helpers, assert `tradeFromMessage` picks up the new bools and `FiatSent()` covers all three signals plus unknown-string tolerance.
2. `healthcheck` package: fake/stub the bisq client dependency — this requires checking whether `HealthChecker` holds the concrete `*bisq.Client`; if so introduce a minimal local interface for the two methods used (`GetPosition`) so tests can inject fakes, matching how `TradeflowRecoverer` is already interfaced. Cases: escalation fires past delay when `FiatSent()`, silent before delay, silent when already escalated (dedupe), silent when notifier/bisq nil, disabled when delay ≤ 0, status never mutated in any case.

### Verification

`go build ./... && go test ./...` in `dexMmController`, then a docs-sync pass on the touched doc pages.

---

## 6. Relevant proto / code references used while planning

### Bisq `Trade.Phase` enum (`dexUnifiedApi/bisq_grpc/pb.proto:1688`)

```
PB_ERROR_PHASE = 0
INIT = 1
TAKER_FEE_PUBLISHED = 2
DEPOSIT_PUBLISHED = 3
DEPOSIT_CONFIRMED = 4
FIAT_SENT = 5
FIAT_RECEIVED = 6
PAYOUT_PUBLISHED = 7
WITHDRAWN = 8
```

### Bisq `Trade.State` enum (relevant slice, `pb.proto:1654`)

```
DEPOSIT_CONFIRMED_IN_BLOCK_CHAIN = 15
BUYER_CONFIRMED_IN_UI_FIAT_PAYMENT_INITIATED = 16
BUYER_SENT_FIAT_PAYMENT_INITIATED_MSG = 17
BUYER_SAW_ARRIVED_FIAT_PAYMENT_INITIATED_MSG = 18
BUYER_STORED_IN_MAILBOX_FIAT_PAYMENT_INITIATED_MSG = 19
BUYER_SEND_FAILED_FIAT_PAYMENT_INITIATED_MSG = 20
SELLER_RECEIVED_FIAT_PAYMENT_INITIATED_MSG = 21
SELLER_CONFIRMED_IN_UI_FIAT_PAYMENT_RECEIPT = 22
SELLER_PUBLISHED_PAYOUT_TX = 23
...
WITHDRAW_COMPLETED = 30
```

### Healthcheck status dispatch (`healthcheck.go:144-151`)

| Status | Handler | Current Bisq behavior |
|---|---|---|
| `DexPositionOpen` | `verifyDexPositionOpen` | chat/payment capture |
| `DexPositionTaken` | `verifyDexPositionTaken` | anomaly-only (`checkBisqAnomaly`) |
| `DexFiatHasArrived` | no-op | BTC release handled by dexrelease after external confirmation |
| `DexPositionReleased` | `verifyDexPositionReleased` | TODO for Bisq / RoboSats |

### Role in the trade

We are the **seller of BTC** (taker of a buy offer). Sequence from our
perspective:

`DEPOSIT_CONFIRMED` → wait buyer sends fiat → buyer marks fiat sent
(`FIAT_SENT`) → our bank scrape (or this new escalation) →
`ConfirmPaymentReceived` → `FIAT_RECEIVED` → `PAYOUT_PUBLISHED` → `WITHDRAWN`.

---

## 7. Status

This document is a record of the conversation and the approved Gap 1 design.
**Nothing in this plan has been implemented yet.**
