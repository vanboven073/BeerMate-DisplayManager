package realtime

import (
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

// TestBroadcastDuringDisconnect exercises a broadcast racing a subscriber
// teardown. Broadcast selects its targets under a read lock and then sends
// outside it, so a client whose channel is closed in between is sent on after
// close.
func TestBroadcastDuringDisconnect(t *testing.T) {
	h := NewHub(64, nil)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			h.NotifySchedule(true, "tick")
		}
	}()

	for i := 0; i < 200; i++ {
		c, err := h.subscribe(RoleAdmin)
		if err != nil {
			t.Fatalf("subscribe: %v", err)
		}
		h.unsubscribe(c)
	}

	close(stop)
	wg.Wait()
}

// TestServeHTTPDisconnectDuringBroadcast drives the same race through the real
// SSE handler rather than the internal helpers.
func TestServeHTTPDisconnectDuringBroadcast(t *testing.T) {
	h := NewHub(64, nil)

	var wg sync.WaitGroup
	stop := make(chan struct{})

	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-stop:
				return
			default:
			}
			h.NotifyMedia("uploaded")
		}
	}()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h.ServeHTTP(w, r, RoleAdmin)
	}))
	defer srv.Close()

	for i := 0; i < 30; i++ {
		req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		buf := make([]byte, 64)
		_, _ = io.ReadFull(resp.Body, buf)
		resp.Body.Close()
		time.Sleep(time.Millisecond)
	}

	close(stop)
	wg.Wait()
}
