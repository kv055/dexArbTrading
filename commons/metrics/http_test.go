package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gorilla/websocket"
)

// TestMiddlewarePreservesHijacker guards against the regression where wrapping
// the ResponseWriter in statusRecorder hid Hijack(), turning every WebSocket
// upgrade behind the middleware into a 500.
func TestMiddlewarePreservesHijacker(t *testing.T) {
	reg := NewServiceRegistry("test")

	mux := http.NewServeMux()
	upgrader := websocket.Upgrader{}
	mux.HandleFunc("GET /ws", func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade failed: %v", err)
			return
		}
		conn.Close()
	})

	srv := httptest.NewServer(reg.Middleware(mux))
	defer srv.Close()

	wsURL := "ws" + srv.URL[len("http"):] + "/ws"
	conn, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		t.Fatalf("websocket dial through metrics middleware failed (status %d): %v", status, err)
	}
	conn.Close()
}
