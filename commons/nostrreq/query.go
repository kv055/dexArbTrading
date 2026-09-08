// Package nostrreq is a short-lived NIP-01 REQ client. Snapshot and fee
// lookups open a WebSocket, collect events until EOSE or timeout, then close.
// It is not a long-lived relay pool.
package nostrreq

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	defaultQueryTimeout = 12 * time.Second
	maxEventsPerRelay   = 5000
)

// Relays used for the public NIP-69 book. Snapshot and fee lookups share these.
// Extra live relays make the merged book more robust; completeness does not
// require every URL to EOSE (see censusComplete). Dial/DNS failures are
// omitted from the completeness denominator.
var MostroRelays = []string{
	"wss://relay.mostro.network",
	"wss://nos.lol",
	"wss://mostro-p2p.tech",
	"wss://relay.damus.io",
	"wss://relay.snort.social",
	"wss://relay.primal.net",
}

// Event is the subset of a Nostr event needed to ingest NIP-69 order books.
type Event struct {
	ID        string     `json:"id"`
	PubKey    string     `json:"pubkey"`
	CreatedAt int64      `json:"created_at"`
	Kind      int        `json:"kind"`
	Tags      [][]string `json:"tags"`
	Content   string     `json:"content"`
}

// TagValue returns the first value for tag name, or "".
func (e Event) TagValue(name string) string {
	for _, tag := range e.Tags {
		if len(tag) >= 2 && tag[0] == name {
			return tag[1]
		}
	}
	return ""
}

// TagValues returns every value after the tag name.
func (e Event) TagValues(name string) []string {
	for _, tag := range e.Tags {
		if len(tag) >= 2 && tag[0] == name {
			out := make([]string, 0, len(tag)-1)
			out = append(out, tag[1:]...)
			return out
		}
	}
	return nil
}

// RelayStat is one relay's outcome from a Query.
type RelayStat struct {
	URL       string
	Events    int
	EOSE      bool
	Err       error
	Connected bool
}

// QueryResult is the merged book plus per-relay census stats.
type QueryResult struct {
	Events    []Event
	Complete  bool
	Relays    []RelayStat
	Connected int
	EOSE      int
}

// Query sends one REQ to each relay and merges the results.
// complete is true when at least two relays sent EOSE, or when every
// successfully dialed relay sent EOSE (so a 1-relay list can still complete).
// Dial/DNS failures are not counted in that denominator. err is set only when
// every relay fails, in which case events is empty.
func Query(ctx context.Context, relays []string, filter map[string]any) ([]Event, bool, error) {
	res, err := QueryWithStats(ctx, relays, filter)
	return res.Events, res.Complete, err
}

// QueryWithStats is Query plus per-relay stats for census logs.
func QueryWithStats(ctx context.Context, relays []string, filter map[string]any) (QueryResult, error) {
	return queryWithDialer(ctx, websocket.DefaultDialer, relays, filter)
}

// QueryWithDialer is Query with an injectable dialer for tests.
func QueryWithDialer(ctx context.Context, dialer *websocket.Dialer, relays []string, filter map[string]any) ([]Event, bool, error) {
	res, err := queryWithDialer(ctx, dialer, relays, filter)
	return res.Events, res.Complete, err
}

func queryWithDialer(ctx context.Context, dialer *websocket.Dialer, relays []string, filter map[string]any) (QueryResult, error) {
	if len(relays) == 0 {
		return QueryResult{}, fmt.Errorf("no relays")
	}
	if dialer == nil {
		dialer = websocket.DefaultDialer
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultQueryTimeout)
		defer cancel()
	}

	type relayResult struct {
		idx    int
		events []Event
		stat   RelayStat
	}
	results := make(chan relayResult, len(relays))
	var wg sync.WaitGroup
	for i, relay := range relays {
		wg.Add(1)
		go func(i int, url string) {
			defer wg.Done()
			events, eose, connected, err := queryRelay(ctx, dialer, url, filter)
			results <- relayResult{
				idx:    i,
				events: events,
				stat: RelayStat{
					URL:       url,
					Events:    len(events),
					EOSE:      eose,
					Err:       err,
					Connected: connected,
				},
			}
		}(i, relay)
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	stats := make([]RelayStat, len(relays))
	var all []Event
	okCount := 0
	var lastErr error
	for result := range results {
		stats[result.idx] = result.stat
		if result.stat.Err != nil {
			lastErr = result.stat.Err
			continue
		}
		okCount++
		all = append(all, result.events...)
	}
	connected, eoseCount := countCensus(stats)
	out := QueryResult{
		Relays:    stats,
		Connected: connected,
		EOSE:      eoseCount,
		Complete:  censusComplete(connected, eoseCount),
	}
	if okCount == 0 {
		if lastErr == nil {
			lastErr = fmt.Errorf("all relays failed")
		}
		return out, lastErr
	}
	out.Events = mergeAddressable(all)
	return out, nil
}

func countCensus(stats []RelayStat) (connected, eose int) {
	for _, s := range stats {
		if s.Connected {
			connected++
		}
		if s.EOSE {
			eose++
		}
	}
	return connected, eose
}

// censusComplete is true if two relays finished (robust union) or every
// connected relay sent EOSE. Relays that never dialed are ignored.
func censusComplete(connected, eose int) bool {
	return eose >= 2 || (connected >= 1 && eose == connected)
}

func queryRelay(ctx context.Context, dialer *websocket.Dialer, url string, filter map[string]any) ([]Event, bool, bool, error) {
	header := http.Header{}
	header.Set("User-Agent", "dexarb-snapshot/nip69")
	conn, _, err := dialer.DialContext(ctx, url, header)
	if err != nil {
		return nil, false, false, err
	}
	defer conn.Close()

	subID := newSubID()
	req, err := json.Marshal([]any{"REQ", subID, filter})
	if err != nil {
		return nil, false, true, err
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(deadline)
		_ = conn.SetReadDeadline(deadline)
	}
	if err := conn.WriteMessage(websocket.TextMessage, req); err != nil {
		return nil, false, true, err
	}

	var events []Event
	eose := false
	for {
		if err := ctx.Err(); err != nil {
			_ = conn.WriteMessage(websocket.TextMessage, closeMsg(subID))
			return events, eose, true, nil
		}
		_, payload, err := conn.ReadMessage()
		if err != nil {
			if len(events) > 0 {
				return events, eose, true, nil
			}
			return nil, false, true, err
		}
		var frame []json.RawMessage
		if err := json.Unmarshal(payload, &frame); err != nil || len(frame) < 2 {
			continue
		}
		var kind string
		if err := json.Unmarshal(frame[0], &kind); err != nil {
			continue
		}
		switch kind {
		case "EVENT":
			if len(frame) < 3 {
				continue
			}
			var event Event
			if err := json.Unmarshal(frame[2], &event); err != nil || event.ID == "" {
				continue
			}
			if len(events) < maxEventsPerRelay {
				events = append(events, event)
			}
		case "EOSE":
			eose = true
			_ = conn.WriteMessage(websocket.TextMessage, closeMsg(subID))
			return events, true, true, nil
		case "CLOSED":
			return events, eose, true, nil
		}
	}
}

func closeMsg(subID string) []byte {
	payload, _ := json.Marshal([]any{"CLOSE", subID})
	return payload
}

func newSubID() string {
	var buf [8]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return fmt.Sprintf("snap%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(buf[:])
}

func mergeAddressable(events []Event) []Event {
	type key struct {
		pub string
		d   string
		id  string
	}
	best := make(map[key]Event, len(events))
	order := make([]key, 0, len(events))
	for _, event := range events {
		k := key{pub: event.PubKey, id: event.ID}
		if event.Kind >= 30000 && event.Kind < 40000 {
			k.d = event.TagValue("d")
			k.id = ""
		}
		prev, ok := best[k]
		if !ok {
			best[k] = event
			order = append(order, k)
			continue
		}
		if event.CreatedAt > prev.CreatedAt || (event.CreatedAt == prev.CreatedAt && event.ID > prev.ID) {
			best[k] = event
		}
	}
	out := make([]Event, 0, len(order))
	for _, k := range order {
		out = append(out, best[k])
	}
	return out
}
