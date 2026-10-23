package relay

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

type heldAdmissions struct {
	handler   http.Handler
	started   chan struct{}
	completed chan *httptest.ResponseRecorder
	requests  sync.WaitGroup
}

func holdAdmissions(t *testing.T, s *Server, releaseEarly bool) *heldAdmissions {
	t.Helper()
	blocked := make(chan struct{})
	h := &heldAdmissions{
		started:   make(chan struct{}, 2*s.cfg.Limits.RequestsPerIP+1),
		completed: make(chan *httptest.ResponseRecorder, 2*s.cfg.Limits.RequestsPerIP+1),
	}
	t.Cleanup(func() {
		close(blocked)
		h.requests.Wait()
	})
	h.handler = s.admit(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if releaseEarly {
			releaseRequestSlot(r)
			releaseRequestSlot(r)
		}
		if r.RemoteAddr != "203.0.113.1:1234" {
			h.started <- struct{}{}
			<-blocked
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	return h
}

func admissionResponse(h http.Handler, source string) *httptest.ResponseRecorder {
	r := httptest.NewRequest("GET", "/.well-known/peerturn", nil)
	r.RemoteAddr = source
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func (h *heldAdmissions) start(t *testing.T, source string) bool {
	t.Helper()
	h.requests.Go(func() { h.completed <- admissionResponse(h.handler, source) })
	select {
	case <-h.started:
		return true
	case w := <-h.completed:
		if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
			t.Fatalf("source overflow status=%d headers=%v", w.Code, w.Header())
		}
		return false
	case <-time.After(5 * time.Second):
		t.Fatal("request did not reach admission decision")
		return false
	}
}

func TestSourceExecutionCapPreservesOtherClients(t *testing.T) {
	f := newFixture(t)
	h := holdAdmissions(t, f.s, false)
	admitted := 0
	for _, source := range []string{"198.51.100.1:1234", "198.51.100.2:1234"} {
		for range f.s.cfg.Limits.RequestsPerIP {
			if h.start(t, source) {
				admitted++
			}
		}
	}
	w := admissionResponse(h.handler, "203.0.113.1:1234")
	if w.Code != http.StatusNoContent {
		t.Fatalf("two sources starved unrelated client: status=%d body=%s", w.Code, w.Body.String())
	}
	if want := 2 * max(1, cap(f.s.slots)/8); admitted != want {
		t.Fatalf("two sources admitted %d executing requests, want %d", admitted, want)
	}
}

func TestExecutionCapTracksGlobalCapacity(t *testing.T) {
	for _, tc := range []struct {
		name        string
		slots, want int
	}{
		{name: "minimum one", slots: 7, want: 1},
		{name: "eighth of slots", slots: 16, want: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newFixture(t)
			f.s.slots = make(chan struct{}, tc.slots)
			h := holdAdmissions(t, f.s, false)
			admitted := 0
			for range f.s.cfg.Limits.RequestsPerIP {
				if h.start(t, "198.51.100.1:1234") {
					admitted++
				}
			}
			if admitted != tc.want {
				t.Fatalf("source admitted %d with %d slots, want %d", admitted, tc.slots, tc.want)
			}
		})
	}
}

func TestExecutionBackpressureReleasesSourceReservation(t *testing.T) {
	f := newFixture(t)
	h := f.s.admit(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	for range cap(f.s.slots) {
		f.s.slots <- struct{}{}
	}
	for range f.s.cfg.Limits.RequestsPerIP + 1 {
		w := admissionResponse(h, "198.51.100.1:1234")
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("global backpressure leaked source reservation: status=%d", w.Code)
		}
	}
	for range cap(f.s.slots) {
		<-f.s.slots
	}
	for range f.s.cfg.Limits.RequestsPerIP + 1 {
		w := admissionResponse(h, "198.51.100.1:1234")
		if w.Code != http.StatusNoContent {
			t.Fatalf("completed request leaked source reservation: status=%d", w.Code)
		}
	}
}

func TestEarlyRequestReleaseAlsoReleasesSourceExecution(t *testing.T) {
	f := newFixture(t)
	early := holdAdmissions(t, f.s, true)
	for range f.s.cfg.Limits.RequestsPerIP + 1 {
		if !early.start(t, "198.51.100.1:1234") {
			t.Fatal("waiting request retained admission or execution capacity")
		}
	}
	if len(f.s.slots) != 0 {
		t.Fatal("waiting requests retained global execution capacity")
	}
	active := holdAdmissions(t, f.s, false)
	limit := max(1, cap(f.s.slots)/8)
	for range limit {
		if !active.start(t, "198.51.100.1:1234") {
			t.Fatal("early release leaked source capacity")
		}
	}
	if active.start(t, "198.51.100.1:1234") {
		t.Fatal("duplicate early release bypassed source execution cap")
	}
}
