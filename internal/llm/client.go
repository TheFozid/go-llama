package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Client wraps the queue for easy integration
type Client struct {
	manager  *Manager
	priority Priority
	timeout  time.Duration
}

// NewClient creates a new queue client
func NewClient(manager *Manager, priority Priority, timeout time.Duration) *Client {
	return &Client{
		manager:  manager,
		priority: priority,
		timeout:  timeout,
	}
}

// Call submits a non-streaming request
func (c *Client) Call(ctx context.Context, url string, payload map[string]interface{}) ([]byte, error) {
	respCh := make(chan *Response, 1)
	errCh := make(chan error, 1)

	req := &Request{
		ID:          fmt.Sprintf("%d_%d", c.priority, time.Now().UnixNano()),
		Priority:    c.priority,
		Context:     ctx,
		URL:         url,
		Payload:     payload,
		IsStreaming: false,
		ResponseCh:  respCh,
		ErrorCh:     errCh,
		SubmitTime:  time.Now(),
		Timeout:     c.timeout,
	}

	if err := c.manager.Submit(req); err != nil {
		return nil, fmt.Errorf("failed to submit: %w", err)
	}

	select {
	case resp := <-respCh:
		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("LLM returned status %d", resp.StatusCode)
		}
		return resp.Body, nil
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// CallStreaming submits a streaming request and returns the HTTP response
func (c *Client) CallStreaming(ctx context.Context, url string, payload map[string]interface{}) (*http.Response, chan struct{}, error) {
	respCh := make(chan *Response, 1)
	errCh := make(chan error, 1)

	req := &Request{
		ID:          fmt.Sprintf("%d_stream_%d", c.priority, time.Now().UnixNano()),
		Priority:    c.priority,
		Context:     ctx,
		URL:         url,
		Payload:     payload,
		IsStreaming: true,
		ResponseCh:  respCh,
		DoneCh: make(chan struct{}),
		ErrorCh:     errCh,
		SubmitTime:  time.Now(),
		Timeout:     c.timeout,
	}

    if err := c.manager.Submit(req); err != nil {
        return nil, nil, fmt.Errorf("failed to submit: %w", err)
    }

    select {
    case resp := <-respCh:
        if resp.StatusCode != http.StatusOK {
            return nil, req.DoneCh, fmt.Errorf("LLM returned status %d", resp.StatusCode)
        }
        return resp.HTTPResp, req.DoneCh, nil
    case err := <-errCh:
        return nil, req.DoneCh, err
    case <-ctx.Done():
        return nil, req.DoneCh, ctx.Err()
    }
}
