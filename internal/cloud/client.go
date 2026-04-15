package cloud

import (
	"bytes"
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
func (c *Client) Check(deviceID string) (*WakeSignal, error) {
	url := fmt.Sprintf("%s/%s/check/%s", c.baseURL, c.token, deviceID)

	var signal WakeSignal
	err := c.doWithRetry("GET", url, nil, &signal)
	if err != nil {
		return nil, err
	}

	if !signal.Wake {
		return nil, nil
	}
	return &signal, nil
}

// Send sends a wake signal to a specific device.
func (c *Client) Send(deviceID string, duration time.Duration) error {
	url := fmt.Sprintf("%s/%s/wake/%s", c.baseURL, c.token, deviceID)
	body := map[string]int{"duration": int(duration.Seconds())}
	return c.doWithRetry("POST", url, body, nil)
}

// SendAll sends a wake signal to all registered devices.
func (c *Client) SendAll(duration time.Duration) error {
	url := fmt.Sprintf("%s/%s/wake?all=true", c.baseURL, c.token)
	body := map[string]int{"duration": int(duration.Seconds())}
	return c.doWithRetry("POST", url, body, nil)
}

// Status returns the status of all devices.
func (c *Client) Status() (map[string]DeviceStatus, error) {
	url := fmt.Sprintf("%s/%s/status", c.baseURL, c.token)

	var resp struct {
		Devices map[string]DeviceStatus `json:"devices"`
	}
	err := c.doWithRetry("GET", url, nil, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Devices, nil
}

// Devices returns all registered devices.
func (c *Client) Devices() (map[string]DeviceInfo, error) {
	url := fmt.Sprintf("%s/%s/devices", c.baseURL, c.token)

	var resp struct {
		Devices map[string]DeviceInfo `json:"devices"`
	}
	err := c.doWithRetry("GET", url, nil, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Devices, nil
}

func (c *Client) doWithRetry(method, url string, reqBody any, result any) error {
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			time.Sleep(2 * time.Second)
		}
		lastErr = c.do(method, url, reqBody, result)
		if lastErr == nil {
			return nil
		}
	}
	return fmt.Errorf("after 3 attempts: %w", lastErr)
}

func (c *Client) do(method, url string, reqBody any, result any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		data, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("marshal request: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, url, bodyReader)
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
