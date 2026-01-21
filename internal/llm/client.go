package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// Client wraps the queue for easy integration
type Client struct {
    manager          *Manager
    priority         Priority
    timeout          time.Duration
    discoveryService *DiscoveryService
}

// NewClient creates a new queue client
func NewClient(manager *Manager, priority Priority, timeout time.Duration, discoveryService *DiscoveryService) *Client {
    return &Client{
        manager:          manager,
        priority:         priority,
        timeout:          timeout,
        discoveryService: discoveryService,
    }
}

// Call submits a non-streaming request
func (c *Client) Call(ctx context.Context, modelName string, payload map[string]interface{}) ([]byte, error) {
    // Find which endpoint has this model
    endpoint := c.discoveryService.FindEndpointForModel(modelName)
    if endpoint == nil {
        return nil, fmt.Errorf("model not found: %s", modelName)
    }
    
    // Construct the appropriate URL based on payload type
    var url string
    if _, ok := payload["messages"]; ok { // Chat completion
        url = endpoint.BaseURL + "/v1/chat/completions"
    } else if _, ok := payload["input"]; ok { // Embedding
        url = endpoint.BaseURL + "/v1/embeddings"
    } else {
        return nil, fmt.Errorf("unable to determine request type from payload")
    }
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
func (c *Client) CallStreaming(ctx context.Context, modelName string, payload map[string]interface{}) (*http.Response, error) {
    // Find which endpoint has this model
    endpoint := c.discoveryService.FindEndpointForModel(modelName)
    if endpoint == nil {
        return nil, fmt.Errorf("model not found: %s", modelName)
    }
    
    // Construct the appropriate URL based on payload type
    var url string
    if _, ok := payload["messages"]; ok { // Chat completion
        url = endpoint.BaseURL + "/v1/chat/completions"
    } else if _, ok := payload["input"]; ok { // Embedding
        url = endpoint.BaseURL + "/v1/embeddings"
    } else {
        return nil, fmt.Errorf("unable to determine request type from payload")
    }
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
		return resp.HTTPResp, nil
	case err := <-errCh:
		return nil, err
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
