package cloud

import (
	"context"
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
		json.NewEncoder(w).Encode(map[string]any{
			"wake": true, "duration": 1800, "created_at": 1000,
		})
	}))
	defer srv.Close()

	ctx := context.Background()
	c := NewClient(srv.URL, "testtoken")
	result, err := c.Check(ctx, "office", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Signal == nil {
		t.Fatal("expected signal, got nil")
	}
	if result.Signal.Duration != 1800 {
		t.Errorf("expected duration 1800, got %d", result.Signal.Duration)
	}
}

func TestCheck_NoSignal(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"wake": false})
	}))
	defer srv.Close()

	ctx := context.Background()
	c := NewClient(srv.URL, "testtoken")
	result, err := c.Check(ctx, "office", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Signal != nil {
		t.Errorf("expected nil signal, got %+v", result.Signal)
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
	err := c.Send(context.Background(), "office", 30*time.Minute)
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
	err := c.SendAll(context.Background(), 30*time.Minute)
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
	devs, err := c.Devices(context.Background())
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
	_, err := c.Check(context.Background(), "office", "")
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
		json.NewEncoder(w).Encode(map[string]any{"wake": true, "duration": 600})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	result, err := c.Check(context.Background(), "office", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Signal == nil {
		t.Fatal("expected signal after retry")
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestCheck_ContextCancelled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow response to allow cancellation
		time.Sleep(2 * time.Second)
		json.NewEncoder(w).Encode(WakeSignal{Wake: true, Duration: 600})
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	c := NewClient(srv.URL, "tok")
	start := time.Now()
	_, err := c.Check(ctx, "office", "")
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if elapsed > 1*time.Second {
		t.Errorf("expected fast cancellation, took %s", elapsed)
	}
}

func TestCheck_WithConfigPiggyback(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cv := r.URL.Query().Get("cv")
		if cv != "abc123" {
			t.Errorf("expected cv=abc123, got %s", cv)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"wake":           false,
			"config":         map[string]any{"check_interval": 120, "default_duration": 1800},
			"config_version": "def456",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	result, err := c.Check(context.Background(), "office", "abc123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Signal != nil {
		t.Errorf("expected no signal, got %+v", result.Signal)
	}
	if result.Config == nil {
		t.Fatal("expected config, got nil")
	}
	if result.Config.CheckInterval != 120 {
		t.Errorf("expected check_interval 120, got %d", result.Config.CheckInterval)
	}
	if result.ConfigVersion != "def456" {
		t.Errorf("expected config_version def456, got %s", result.ConfigVersion)
	}
}

func TestCheck_NoConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode(map[string]any{"wake": false})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	result, err := c.Check(context.Background(), "office", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result.Config != nil {
		t.Errorf("expected no config, got %+v", result.Config)
	}
}

func TestPushGlobalConfig(t *testing.T) {
	var gotPath, gotMethod string
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		json.NewDecoder(r.Body).Decode(&gotBody)
		json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"config":  map[string]any{"check_interval": 120},
			"version": "v1",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	resp, err := c.PushGlobalConfig(context.Background(), &RemoteConfig{CheckInterval: 120})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != "PUT" {
		t.Errorf("expected PUT, got %s", gotMethod)
	}
	if gotPath != "/tok/config" {
		t.Errorf("expected path /tok/config, got %s", gotPath)
	}
	if resp.Version != "v1" {
		t.Errorf("expected version v1, got %s", resp.Version)
	}
}

func TestPushDeviceConfig(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		json.NewEncoder(w).Encode(map[string]any{
			"ok":      true,
			"config":  map[string]any{"ac_check_interval": 60},
			"version": "v2",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	_, err := c.PushDeviceConfig(context.Background(), "mac-mini", &RemoteConfig{ACCheckInterval: 60})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotPath != "/tok/config/mac-mini" {
		t.Errorf("expected path /tok/config/mac-mini, got %s", gotPath)
	}
}

func TestGetGlobalConfig(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			t.Errorf("expected GET, got %s", r.Method)
		}
		json.NewEncoder(w).Encode(map[string]any{
			"config":  map[string]any{"check_interval": 120, "default_duration": 1800},
			"version": "v1",
		})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	resp, err := c.GetGlobalConfig(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Config.CheckInterval != 120 {
		t.Errorf("expected check_interval 120, got %d", resp.Config.CheckInterval)
	}
}

func TestDeleteDeviceConfig(t *testing.T) {
	var gotPath, gotMethod string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		json.NewEncoder(w).Encode(map[string]any{"ok": true})
	}))
	defer srv.Close()

	c := NewClient(srv.URL, "tok")
	err := c.DeleteDeviceConfig(context.Background(), "mac-mini")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotMethod != "DELETE" {
		t.Errorf("expected DELETE, got %s", gotMethod)
	}
	if gotPath != "/tok/config/mac-mini" {
		t.Errorf("expected path /tok/config/mac-mini, got %s", gotPath)
	}
}
