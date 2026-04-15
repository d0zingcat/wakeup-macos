package cloud

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCheck_WithSignal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.Path != "/testtoken/check/office" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		json.NewEncoder(w).Encode(WakeSignal{Wake: true, Duration: 1800, CreatedAt: 1000})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "testtoken")
	sig, err := c.Check("office")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sig == nil {
		t.Fatal("expected signal, got nil")
	}
	if sig.Duration != 1800 {
		t.Errorf("expected duration 1800, got %d", sig.Duration)
	}
}

func TestCheck_NoSignal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(WakeSignal{Wake: false})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "testtoken")
	sig, err := c.Check("office")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sig != nil {
		t.Errorf("expected nil signal, got %+v", sig)
	}
}

func TestSend(t *testing.T) {
	var gotPath string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	err := c.Send("office", 30*time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/tok/wake/office" {
		t.Errorf("expected path /tok/wake/office, got %s", gotPath)
	}
	if int(gotBody["duration"].(float64)) != 1800 {
		t.Errorf("expected duration 1800, got %v", gotBody["duration"])
	}
}

func TestSendAll(t *testing.T) {
	var gotQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotQuery = r.URL.RawQuery
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	err := c.SendAll(30 * time.Minute)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotQuery != "all=true" {
		t.Errorf("expected query all=true, got %s", gotQuery)
	}
}

func TestDevices(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{
			"devices": map[string]any{
				"office":    map[string]any{"last_seen": 1000},
				"home-mini": map[string]any{"last_seen": 2000},
			},
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	devs, err := c.Devices()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(devs) != 2 {
		t.Errorf("expected 2 devices, got %d", len(devs))
	}
}

func TestCheck_404(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(404)
		w.Write([]byte(`{"error":"not found"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "badtoken")
	_, err := c.Check("office")
	if err == nil {
		t.Fatal("expected error for 404")
	}
}

func TestCheck_Retry(t *testing.T) {
	attempts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts++
		if attempts < 3 {
			w.WriteHeader(500)
			w.Write([]byte(`{"error":"server error"}`))
			return
		}
		json.NewEncoder(w).Encode(WakeSignal{Wake: true, Duration: 600})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	sig, err := c.Check("office")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if sig == nil {
		t.Fatal("expected signal after retry")
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}
