# PushPush Relay — Pairing QR Contract (v2)

Audience: coding agent implementing the **PushPush Relay** Android app.
This document specifies the pairing QR code that the dexMonitor dashboard
renders and the relay app must scan, parse, and act on.

## 1. Where the QR comes from

1. The dashboard calls `GET /api/phone-push/setup` (authenticated session).
   The response contains the callback URLs, heartbeat metadata, and the list
   of authorized payment methods (`payment_methods`).
2. On explicit user action ("Reveal pairing QR") the dashboard calls
   `POST /api/phone-push/setup/reveal` to obtain `shared_secret`.
3. The frontend builds a JSON payload and renders it as a QR code
   (byte mode, error correction level **M**, margin 2 — see
   `dexMonitor/react-frontend/src/components/LocalQrCode.tsx`).

Payload construction reference: `pairingQrPayload` in
`dexMonitor/react-frontend/src/components/PhonePushSetupSection.tsx`.

## 2. QR payload format

The QR content is a single UTF-8 JSON object, no wrapper, no deep link:

```json
{
  "v": 2,
  "type": "pushpush-pairing",
  "notify_url": "https://dexarb.example.org/pushpush/notify",
  "heartbeat_url": "https://dexarb.example.org/pushpush/phone-heartbeat",
  "secret": "<shared webhook secret>",
  "payment_methods": [
    {
      "id": 17,
      "name": "Revolut",
      "category": "Fiat Transmitter",
      "alias_list": ["Revolut UAB", "revolut"],
      "sources": ["binance_p2p", "kraken"],
      "currencies": ["EUR", "USD"]
    }
  ]
}
```

### Top-level fields

| Field             | Type     | Req | Description |
|-------------------|----------|-----|-------------|
| `v`               | int      | yes | Payload schema version. `2` since payment methods were added; `1` payloads have no `payment_methods`. |
| `type`            | string   | yes | Constant `"pushpush-pairing"`. Reject the payload if it differs. |
| `notify_url`      | string   | yes | Absolute HTTPS URL. Target for payment-notification POSTs (banking app notifications forwarded by the relay). |
| `heartbeat_url`   | string   | yes | Absolute HTTPS URL. Target for periodic heartbeat POSTs. |
| `secret`          | string   | yes | Shared secret. Send as HTTP header `X-Webhook-Secret` on **every** request to both endpoints. Treat as sensitive: never log, never persist in plaintext backups. |
| `payment_methods` | array    | yes* | Linked + active payment methods (see below). *Present from v2 on; may be an empty array. |

### `payment_methods[]` entries

Each entry mirrors one row of the `payment_methods` SQL table where
`is_linked AND is_active` is true (`is_linked` is a generated column equal to
`account_id IS NOT NULL`; activation is derived server-side from a linked
payment account). Source query:
`paymentmethods.Repo.ListLinkedActive` in
`dexMonitor/internal/paymentmethods/repo.go`.

| Field        | Type     | Description |
|--------------|----------|-------------|
| `id`         | int64    | Stable catalog ID of the payment method. |
| `name`       | string   | Canonical provider name, e.g. `"Revolut"`, `"Deutsche Bank"`. |
| `category`   | string   | One of the catalog categories; linked+active rows are always `"Bank"` or `"Fiat Transmitter"` (enforced by a table CHECK constraint). |
| `alias_list` | string[] | Alternative provider names that must be treated as equivalent to `name` when matching notification text/sender labels. May be empty. |
| `sources`    | string[] | DEX/P2P providers where this method has been observed (informational). |
| `currencies` | string[] | Fiat currencies observed for this method, ISO codes such as `"EUR"` (informational/filtering hint). |

### Semantics for the app

- A payment method present in `payment_methods` is an **authorized fiat
  rail**: notifications whose text matches its `name` or any of its
  `alias_list` entries (case-insensitive comparison recommended) should be
  forwarded via `notify_url`.
- Methods absent from the list are not authorized; do not forward
  notifications matched only against them.
- An empty array means pairing succeeds but nothing is authorized yet; the
  operator links/activates methods later in the dashboard. Re-pair (or
  provide a re-scan flow) to refresh the list.

## 3. Compatibility rules

- **Unknown fields must be ignored.** New top-level or per-entry fields may be
  added in future minor revisions without bumping `v`.
- Apps written against v1 keep working: they ignore `payment_methods`.
- A v2-aware app should accept `v >= 2` payloads with `type =
  "pushpush-pairing"` and reject anything else with a clear error.
- If `payment_methods` is missing while `v == 1`, behave like v1 (pair
  without authorization data).

## 4. Size constraints

QR byte-mode capacity at ECC level M caps out around 2.3 KB. The server keeps
entries compact, but operators can link many methods with long alias lists.
The app therefore MUST:

- Parse the full JSON without assuming a maximum entry count.
- Surface a clear error if scanning yields invalid/truncated JSON rather than
  silently pairing with partial data.
- Not require the payment-method list to be non-empty to complete pairing.

## 5. Runtime contract after pairing

Unchanged from v1:

- **Notify:** `POST {notify_url}`, header `X-Webhook-Secret: {secret}`,
  `Content-Type: application/json`,
  body `{"app":"<bank-app>","title":"<title>","text":"<text>","time":"<optional ISO-8601>"}`.
- **Heartbeat:** `POST {heartbeat_url}` every 5 minutes, same secret header,
  body `{"device_id":"<stable-phone-name>"}`. The server marks the phone
  offline after `max_age_seconds` without a heartbeat (default 15 min).

## 6. Related source files

| Concern | File |
|---------|------|
| QR payload construction | `dexMonitor/react-frontend/src/components/PhonePushSetupSection.tsx` |
| QR rendering | `dexMonitor/react-frontend/src/components/LocalQrCode.tsx` |
| Setup API types (TS) | `dexMonitor/react-frontend/src/api/communication.ts` |
| Setup endpoint + response struct | `dexMonitor/cmd/server/main.go` (`handlePhonePushSetup`, `phonePushSetup`) |
| Payment-methods SQL query | `dexMonitor/internal/paymentmethods/repo.go` (`ListLinkedActive`) |
| Table schema | `dexUnifiedApi/db/schema/20_payment_methods.sql` |
| Receiver endpoints | `dexFiatController/internal/pushpush/` |
