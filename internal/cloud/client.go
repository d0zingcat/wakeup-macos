package cloud

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	baseURL    string
	token      string
	httpClient *http.Client
}

type WakeSignal struct {
	Wake      bool  `json:"wake"`
	Duration  int   `json:"duration"`
	CreatedAt int64 `json:"created_at"`
}

// CheckResult holds the response from a /check call, including optional remote config.
type CheckResult struct {
	Signal        *WakeSignal
	Config        *RemoteConfig
	ConfigVersion string
}

// RemoteConfig contains fields that can be managed remotely.
// Zero values mean "not set" (will not override local config).
type RemoteConfig struct {
	CheckInterval           int   `json:"check_interval,omitempty"`            // seconds
	DefaultDuration         int   `json:"default_duration,omitempty"`          // seconds
	ACCheckInterval         int   `json:"ac_check_interval,omitempty"`         // seconds
	BatteryCheckInterval    int   `json:"battery_check_interval,omitempty"`    // seconds
	EnableDarkwakeDetection *bool `json:"enable_darkwake_detection,omitempty"` // pointer to distinguish false from unset
	WakeDetectInterval      int   `json:"wake_detect_interval,omitempty"`      // seconds
}

// ConfigResponse is the response from config GET endpoints.
type ConfigResponse struct {
	Config  RemoteConfig `json:"config"`
	Version string       `json:"version"`
}

type DeviceStatus struct {
	LastSeen    int64 `json:"last_seen"`
	PendingWake bool  `json:"pending_wake"`
}

type DeviceInfo struct {
	LastSeen int64 `json:"last_seen"`
}

func NewClient(baseURL, token string) *Client {
	return &Client{
		baseURL: baseURL,
		token:   token,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

// Check reads and clears the wake signal for a device.
// If configVersion is non-empty, it is sent as the cv query parameter
// for conditional config delivery.
func (c *Client) Check(ctx context.Context, deviceID string, configVersion string) (*CheckResult, error) {
	url := fmt.Sprintf("%s/%s/check/%s", c.baseURL, c.token, deviceID)
	if configVersion != "" {
		url += "?cv=" + configVersion
	}

	var raw json.RawMessage
	err := c.doWithRetry(ctx, "GET", url, nil, &raw)
	if err != nil {
		return nil, err
	}

	// Parse the response which may contain wake signal + optional config
	var resp struct {
		Wake          bool          `json:"wake"`
		Duration      int           `json:"duration"`
		CreatedAt     int64         `json:"created_at"`
		Config        *RemoteConfig `json:"config,omitempty"`
		ConfigVersion string        `json:"config_version,omitempty"`
	}
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode check response: %w", err)
	}

	result := &CheckResult{
		Config:        resp.Config,
		ConfigVersion: resp.ConfigVersion,
	}
	if resp.Wake {
		result.Signal = &WakeSignal{
			Wake:      true,
			Duration:  resp.Duration,
			CreatedAt: resp.CreatedAt,
		}
	}
	return result, nil
}

// Send sends a wake signal to a specific device.
func (c *Client) Send(ctx context.Context, deviceID string, duration time.Duration) error {
	url := fmt.Sprintf("%s/%s/wake/%s", c.baseURL, c.token, deviceID)
	body := map[string]int{"duration": int(duration.Seconds())}
	return c.doWithRetry(ctx, "POST", url, body, nil)
}

// SendAll sends a wake signal to all registered devices.
func (c *Client) SendAll(ctx context.Context, duration time.Duration) error {
	url := fmt.Sprintf("%s/%s/wake?all=true", c.baseURL, c.token)
	body := map[string]int{"duration": int(duration.Seconds())}
	return c.doWithRetry(ctx, "POST", url, body, nil)
}

// Status returns the status of all devices.
func (c *Client) Status(ctx context.Context) (map[string]DeviceStatus, error) {
	url := fmt.Sprintf("%s/%s/status", c.baseURL, c.token)

	var resp struct {
		Devices map[string]DeviceStatus `json:"devices"`
	}
	err := c.doWithRetry(ctx, "GET", url, nil, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Devices, nil
}

// Devices returns all registered devices.
func (c *Client) Devices(ctx context.Context) (map[string]DeviceInfo, error) {
	url := fmt.Sprintf("%s/%s/devices", c.baseURL, c.token)

	var resp struct {
		Devices map[string]DeviceInfo `json:"devices"`
	}
	err := c.doWithRetry(ctx, "GET", url, nil, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Devices, nil
}

// PushGlobalConfig sets the global remote config.
func (c *Client) PushGlobalConfig(ctx context.Context, cfg *RemoteConfig) (*ConfigResponse, error) {
	url := fmt.Sprintf("%s/%s/config", c.baseURL, c.token)
	var resp ConfigResponse
	err := c.doWithRetry(ctx, "PUT", url, cfg, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// PushDeviceConfig sets a device-specific remote config.
func (c *Client) PushDeviceConfig(ctx context.Context, deviceID string, cfg *RemoteConfig) (*ConfigResponse, error) {
	url := fmt.Sprintf("%s/%s/config/%s", c.baseURL, c.token, deviceID)
	var resp ConfigResponse
	err := c.doWithRetry(ctx, "PUT", url, cfg, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetGlobalConfig retrieves the global remote config.
func (c *Client) GetGlobalConfig(ctx context.Context) (*ConfigResponse, error) {
	url := fmt.Sprintf("%s/%s/config", c.baseURL, c.token)
	var resp ConfigResponse
	err := c.doWithRetry(ctx, "GET", url, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// GetDeviceConfig retrieves a device-specific remote config.
func (c *Client) GetDeviceConfig(ctx context.Context, deviceID string) (*ConfigResponse, error) {
	url := fmt.Sprintf("%s/%s/config/%s", c.baseURL, c.token, deviceID)
	var resp ConfigResponse
	err := c.doWithRetry(ctx, "GET", url, nil, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// DeleteDeviceConfig removes a device-specific remote config.
func (c *Client) DeleteDeviceConfig(ctx context.Context, deviceID string) error {
	url := fmt.Sprintf("%s/%s/config/%s", c.baseURL, c.token, deviceID)
	return c.doWithRetry(ctx, "DELETE", url, nil, nil)
}

func (c *Client) doWithRetry(ctx context.Context, method, url string, reqBody any, result any) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(2 * time.Second):
			}
		}
		lastErr = c.do(ctx, method, url, reqBody, result)
		if lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	return fmt.Errorf("after 3 attempts: %w", lastErr)
}

func (c *Client) do(ctx context.Context, method, url string, reqBody any, result any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, url, bodyReader)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == 404 {
		return fmt.Errorf("not found (check worker_url and token)")
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBody))
	}

	if result != nil {
		if err := json.Unmarshal(respBody, result); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}
