package nostrreq

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

var upgrader = websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}

func TestMostroRelaysAreUniqueLiveSet(t *testing.T) {
	want := []string{
		"wss://relay.mostro.network",
		"wss://nos.lol",
		"wss://mostro-p2p.tech",
		"wss://relay.damus.io",
		"wss://relay.snort.social",
		"wss://relay.primal.net",
	}
	if len(MostroRelays) != len(want) {
		t.Fatalf("MostroRelays = %#v, want %#v", MostroRelays, want)
	}
	seen := map[string]bool{}
	for i, url := range MostroRelays {
		if url != want[i] {
			t.Fatalf("MostroRelays[%d] = %q, want %q", i, url, want[i])
		}
		if seen[url] {
			t.Fatalf("duplicate relay %q", url)
		}
		seen[url] = true
		if strings.HasSuffix(url, "/") {
			t.Fatalf("relay %q has trailing slash", url)
		}
	}
}

func TestQueryCollectsEOSEAndMergesReplaceableEvents(t *testing.T) {
	older := Event{
		ID: "a", PubKey: "pub", CreatedAt: 1, Kind: 38383,
		Tags: [][]string{{"d", "order-1"}, {"s", "pending"}},
	}
	newer := Event{
		ID: "b", PubKey: "pub", CreatedAt: 2, Kind: 38383,
		Tags: [][]string{{"d", "order-1"}, {"s", "pending"}},
	}
	other := Event{
		ID: "c", PubKey: "pub", CreatedAt: 2, Kind: 38383,
		Tags: [][]string{{"d", "order-2"}, {"s", "pending"}},
	}

	srv1 := nostrServer(t, []Event{older, other}, true)
	defer srv1.Close()
	srv2 := nostrServer(t, []Event{newer}, true)
	defer srv2.Close()

	events, complete, err := Query(context.Background(), []string{wsURL(srv1), wsURL(srv2)}, map[string]any{
		"kinds": []int{38383},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if !complete {
		t.Fatal("expected complete census")
	}
	if len(events) != 2 {
		t.Fatalf("events = %d, want 2 after replaceable merge", len(events))
	}
	byD := map[string]Event{}
	for _, event := range events {
		byD[event.TagValue("d")] = event
	}
	if byD["order-1"].ID != "b" {
		t.Fatalf("order-1 id = %q, want newer event b", byD["order-1"].ID)
	}
	if byD["order-2"].ID != "c" {
		t.Fatalf("order-2 id = %q, want c", byD["order-2"].ID)
	}
}

func TestCensusCompleteRule(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name            string
		connected, eose int
		want            bool
	}{
		{"two of four eose", 4, 2, true},
		{"one of four eose", 4, 1, false},
		{"zero eose with connections", 2, 0, false},
		{"single connected eose", 1, 1, true},
		{"two connected both eose", 2, 2, true},
		{"none connected", 0, 0, false},
		{"four connected one eose", 4, 1, false},
		{"four connected three eose", 4, 3, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := censusComplete(tc.connected, tc.eose); got != tc.want {
				t.Fatalf("censusComplete(%d, %d) = %v, want %v", tc.connected, tc.eose, got, tc.want)
			}
		})
	}
}

func TestQueryCompleteWhenExtraRelayDialFails(t *testing.T) {
	ok1 := nostrServer(t, []Event{{ID: "a", PubKey: "pub", CreatedAt: 1, Kind: 1}}, true)
	defer ok1.Close()
	ok2 := nostrServer(t, []Event{{ID: "b", PubKey: "pub", CreatedAt: 1, Kind: 1}}, true)
	defer ok2.Close()

	res, err := QueryWithStats(context.Background(), []string{wsURL(ok1), wsURL(ok2), "ws://127.0.0.1:1"}, map[string]any{
		"kinds": []int{1},
	})
	if err != nil {
		t.Fatalf("QueryWithStats: %v", err)
	}
	if !res.Complete {
		t.Fatalf("complete = false, connected=%d eose=%d; dial-fail must not mark a 2-EOSE census incomplete", res.Connected, res.EOSE)
	}
	if res.Connected != 2 || res.EOSE != 2 {
		t.Fatalf("connected=%d eose=%d, want 2/2 (dial-fail excluded)", res.Connected, res.EOSE)
	}
	if len(res.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(res.Events))
	}
	if len(res.Relays) != 3 || res.Relays[2].Connected || res.Relays[2].Err == nil {
		t.Fatalf("dial-fail relay stats = %#v", res.Relays)
	}
}

func TestQueryTwoOfFourEOSEIsComplete(t *testing.T) {
	s1 := nostrServer(t, []Event{{ID: "a", PubKey: "p", CreatedAt: 1, Kind: 1}}, true)
	s2 := nostrServer(t, []Event{{ID: "b", PubKey: "p", CreatedAt: 1, Kind: 1}}, true)
	s3 := nostrServer(t, []Event{{ID: "c", PubKey: "p", CreatedAt: 1, Kind: 1}}, false)
	s4 := nostrServer(t, []Event{{ID: "d", PubKey: "p", CreatedAt: 1, Kind: 1}}, false)
	defer s1.Close()
	defer s2.Close()
	defer s3.Close()
	defer s4.Close()

	res, err := QueryWithStats(context.Background(), []string{wsURL(s1), wsURL(s2), wsURL(s3), wsURL(s4)}, map[string]any{"kinds": []int{1}})
	if err != nil {
		t.Fatalf("QueryWithStats: %v", err)
	}
	if !res.Complete {
		t.Fatalf("2 of 4 EOSE should be complete; connected=%d eose=%d", res.Connected, res.EOSE)
	}
	if res.Connected != 4 || res.EOSE != 2 {
		t.Fatalf("connected=%d eose=%d, want 4/2", res.Connected, res.EOSE)
	}
}

func TestQueryOneOfFourEOSEIsIncomplete(t *testing.T) {
	s1 := nostrServer(t, []Event{{ID: "a", PubKey: "p", CreatedAt: 1, Kind: 1}}, true)
	s2 := nostrServer(t, []Event{{ID: "b", PubKey: "p", CreatedAt: 1, Kind: 1}}, false)
	s3 := nostrServer(t, []Event{{ID: "c", PubKey: "p", CreatedAt: 1, Kind: 1}}, false)
	s4 := nostrServer(t, []Event{{ID: "d", PubKey: "p", CreatedAt: 1, Kind: 1}}, false)
	defer s1.Close()
	defer s2.Close()
	defer s3.Close()
	defer s4.Close()

	res, err := QueryWithStats(context.Background(), []string{wsURL(s1), wsURL(s2), wsURL(s3), wsURL(s4)}, map[string]any{"kinds": []int{1}})
	if err != nil {
		t.Fatalf("QueryWithStats: %v", err)
	}
	if res.Complete {
		t.Fatalf("1 of 4 EOSE should be incomplete; connected=%d eose=%d", res.Connected, res.EOSE)
	}
	if res.Connected != 4 || res.EOSE != 1 {
		t.Fatalf("connected=%d eose=%d, want 4/1", res.Connected, res.EOSE)
	}
	if len(res.Events) == 0 {
		t.Fatal("expected events from the working relays")
	}
}

func TestQueryZeroEOSEWithEventsIsIncomplete(t *testing.T) {
	s1 := nostrServer(t, []Event{{ID: "a", PubKey: "p", CreatedAt: 1, Kind: 1}}, false)
	s2 := nostrServer(t, []Event{{ID: "b", PubKey: "p", CreatedAt: 1, Kind: 1}}, false)
	defer s1.Close()
	defer s2.Close()

	res, err := QueryWithStats(context.Background(), []string{wsURL(s1), wsURL(s2)}, map[string]any{"kinds": []int{1}})
	if err != nil {
		t.Fatalf("QueryWithStats: %v", err)
	}
	if res.Complete {
		t.Fatal("0 EOSE with events should be incomplete")
	}
	if len(res.Events) == 0 {
		t.Fatal("expected events even when incomplete")
	}
}

func TestQuerySingleRelayEOSEIsComplete(t *testing.T) {
	ok := nostrServer(t, []Event{{ID: "x", PubKey: "pub", CreatedAt: 1, Kind: 1}}, true)
	defer ok.Close()

	res, err := QueryWithStats(context.Background(), []string{wsURL(ok)}, map[string]any{"kinds": []int{1}})
	if err != nil {
		t.Fatalf("QueryWithStats: %v", err)
	}
	if !res.Complete || res.Connected != 1 || res.EOSE != 1 {
		t.Fatalf("single-relay EOSE should complete; complete=%v connected=%d eose=%d", res.Complete, res.Connected, res.EOSE)
	}
}

func TestQueryAllRelaysFailed(t *testing.T) {
	res, err := QueryWithStats(context.Background(), []string{"ws://127.0.0.1:1", "ws://127.0.0.1:2"}, map[string]any{"kinds": []int{1}})
	if err == nil {
		t.Fatal("expected error when every relay fails")
	}
	if res.Complete || len(res.Events) != 0 {
		t.Fatalf("all dial-fail result = complete=%v events=%d, want empty + incomplete", res.Complete, len(res.Events))
	}
	if res.Connected != 0 {
		t.Fatalf("connected=%d, want 0", res.Connected)
	}
}

func nostrServer(t *testing.T, events []Event, sendEOSE bool) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var frame []json.RawMessage
		if err := json.Unmarshal(payload, &frame); err != nil || len(frame) < 2 {
			return
		}
		var subID string
		_ = json.Unmarshal(frame[1], &subID)
		for _, event := range events {
			msg, _ := json.Marshal([]any{"EVENT", subID, event})
			if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
				return
			}
		}
		if sendEOSE {
			msg, _ := json.Marshal([]any{"EOSE", subID})
			_ = conn.WriteMessage(websocket.TextMessage, msg)
		}
		time.Sleep(20 * time.Millisecond)
	}))
}

func wsURL(s *httptest.Server) string {
	return "ws" + strings.TrimPrefix(s.URL, "http")
}
