package llm

import (
    "bytes"
    "encoding/json"
    "fmt"
    "log"
    "net/http"
    "strings"
    "sync"
    "time"

    "go-llama/internal/config"
)

// DiscoveryService manages dynamic model discovery and caching
type DiscoveryService struct {
    endpoints map[string]*LLMEndpoint
    config    *config.Config
    mutex     sync.RWMutex
    stopCh    chan struct{}
    wg        sync.WaitGroup
}

// NewDiscoveryService creates a new discovery service
func NewDiscoveryService(cfg *config.Config) *DiscoveryService {
    d := &DiscoveryService{
        endpoints: make(map[string]*LLMEndpoint),
        config:    cfg,
        stopCh:    make(chan struct{}),
    }

    // Initialize endpoints from config
    for _, llmCfg := range cfg.LLMs {
        d.endpoints[llmCfg.URL] = &LLMEndpoint{
            BaseURL:    llmCfg.URL,
            IsOnline:   false,
            ErrorCount: 0,
        }
    }

    // Add GrowerAI endpoints if configured
    if cfg.GrowerAI.ReasoningModel.BaseURL != "" {
        d.endpoints[cfg.GrowerAI.ReasoningModel.BaseURL] = &LLMEndpoint{
            BaseURL:    cfg.GrowerAI.ReasoningModel.BaseURL,
            IsOnline:   false,
            ErrorCount: 0,
        }
    }
    if cfg.GrowerAI.EmbeddingModel.BaseURL != "" {
        d.endpoints[cfg.GrowerAI.EmbeddingModel.BaseURL] = &LLMEndpoint{
            BaseURL:    cfg.GrowerAI.EmbeddingModel.BaseURL,
            IsOnline:   false,
            ErrorCount: 0,
        }
    }
    if cfg.GrowerAI.Compression.Model.BaseURL != "" {
        d.endpoints[cfg.GrowerAI.Compression.Model.BaseURL] = &LLMEndpoint{
            BaseURL:    cfg.GrowerAI.Compression.Model.BaseURL,
            IsOnline:   false,
            ErrorCount: 0,
        }
    }

    return d
}

// Start begins the background refresh routine
func (d *DiscoveryService) Start() {
    d.wg.Add(1)
    go d.backgroundRefresh()
    log.Printf("[Discovery] Started model discovery service")
}

// Stop stops the discovery service
func (d *DiscoveryService) Stop() {
    close(d.stopCh)
    d.wg.Wait()
    log.Printf("[Discovery] Stopped model discovery service")
}

// backgroundRefresh periodically refreshes all endpoints
func (d *DiscoveryService) backgroundRefresh() {
    defer d.wg.Done()

    ticker := time.NewTicker(5 * time.Minute) // Default refresh interval
    defer ticker.Stop()

    // Initial refresh
    d.RefreshAllEndpoints()

    for {
        select {
        case <-d.stopCh:
            return
        case <-ticker.C:
            d.RefreshAllEndpoints()
        }
    }
}

// RefreshAllEndpoints refreshes all configured endpoints
func (d *DiscoveryService) RefreshAllEndpoints() {
    d.mutex.RLock()
    endpoints := make([]string, 0, len(d.endpoints))
    for url := range d.endpoints {
        endpoints = append(endpoints, url)
    }
    d.mutex.RUnlock()

    for _, url := range endpoints {
        if err := d.RefreshEndpoint(url); err != nil {
            log.Printf("[Discovery] Failed to refresh endpoint %s: %v", url, err)
        }
    }
}

// RefreshEndpoint fetches model information from a specific endpoint
func (d *DiscoveryService) RefreshEndpoint(baseURL string) error {
    d.mutex.Lock()
    endpoint, exists := d.endpoints[baseURL]
    if !exists {
        endpoint = &LLMEndpoint{
            BaseURL:    baseURL,
            IsOnline:   false,
            ErrorCount: 0,
        }
        d.endpoints[baseURL] = endpoint
    }
    d.mutex.Unlock()

    // Fetch models from /v1/models
    models, err := d.fetchModels(baseURL)
    if err != nil {
        endpoint.mutex.Lock()
        endpoint.IsOnline = false
        endpoint.ErrorCount++
        endpoint.mutex.Unlock()
        return fmt.Errorf("failed to fetch models: %w", err)
    }

    // Test each model for capabilities
    for i := range models {
        models[i].IsChat = d.testModelCapability(baseURL, models[i].Name, "chat")
        models[i].IsEmbedding = d.testModelCapability(baseURL, models[i].Name, "embedding")
        models[i].LastFetched = time.Now()
    }

    // Update endpoint
    endpoint.mutex.Lock()
    endpoint.Models = models
    endpoint.LastUpdated = time.Now()
    endpoint.IsOnline = true
    endpoint.ErrorCount = 0
    endpoint.mutex.Unlock()

    log.Printf("[Discovery] Refreshed endpoint %s: found %d models", baseURL, len(models))
    return nil
}

// fetchModels fetches the model list from /v1/models
func (d *DiscoveryService) fetchModels(baseURL string) ([]ModelInfo, error) {
    client := &http.Client{Timeout: 10 * time.Second}
    resp, err := client.Get(baseURL + "/v1/models")
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
    }

    var result struct {
        Object string      `json:"object"`
        Data   []ModelInfo `json:"data"`
    }

    if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
        return nil, fmt.Errorf("failed to decode response: %w", err)
    }

    return result.Data, nil
}

// testModelCapability tests if a model supports a specific capability
func (d *DiscoveryService) testModelCapability(baseURL, modelName, capability string) bool {
    var url string
    var payload map[string]interface{}

    switch capability {
    case "chat":
        url = baseURL + "/v1/chat/completions"
        payload = map[string]interface{}{
            "model": modelName,
            "messages": []map[string]string{
                {"role": "user", "content": "test"},
            },
            "max_tokens": 1,
        }
    case "embedding":
        url = baseURL + "/v1/embeddings"
        payload = map[string]interface{}{
            "model": modelName,
            "input": "test",
        }
    default:
        return false
    }

    jsonData, _ := json.Marshal(payload)
    req, _ := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
    req.Header.Set("Content-Type", "application/json")

    client := &http.Client{Timeout: 5 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return false
    }
    defer resp.Body.Close()

    // Consider it successful if we get a 200 or a model-specific error (not a "not found" error)
    return resp.StatusCode == http.StatusOK || 
           (resp.StatusCode >= 400 && resp.StatusCode < 500 && 
            !strings.Contains(resp.Status, "not found") && 
            !strings.Contains(resp.Status, "does not exist"))
}

// GetModels returns cached models for an endpoint
func (d *DiscoveryService) GetModels(baseURL string) ([]ModelInfo, error) {
    d.mutex.RLock()
    endpoint, exists := d.endpoints[baseURL]
    d.mutex.RUnlock()

    if !exists {
        return nil, fmt.Errorf("endpoint not configured: %s", baseURL)
    }

    endpoint.mutex.RLock()
    models := endpoint.Models
    lastUpdated := endpoint.LastUpdated
    isOnline := endpoint.IsOnline
    endpoint.mutex.RUnlock()

    // If models are empty or stale, refresh
    if len(models) == 0 || time.Since(lastUpdated) > 5*time.Minute {
        if err := d.RefreshEndpoint(baseURL); err != nil {
            // Return stale data if available and configured to do so
            if len(models) > 0 {
                log.Printf("[Discovery] Using stale models for %s due to refresh error", baseURL)
                return models, nil
            }
            return nil, err
        }
        
        // Get updated models
        endpoint.mutex.RLock()
        models = endpoint.Models
        endpoint.mutex.RUnlock()
    }

    if !isOnline {
        return nil, fmt.Errorf("endpoint offline: %s", baseURL)
    }

    return models, nil
}

// FindEndpointForModel finds which endpoint has a specific model
func (d *DiscoveryService) FindEndpointForModel(modelName string) *LLMEndpoint {
    d.mutex.RLock()
    defer d.mutex.RUnlock()

    for _, endpoint := range d.endpoints {
        endpoint.mutex.RLock()
        for _, model := range endpoint.Models {
            if model.Name == modelName {
                endpoint.mutex.RUnlock()
                return endpoint
            }
        }
        endpoint.mutex.RUnlock()
    }
    return nil
}

// GetChatModels returns all models that support chat completions
func (d *DiscoveryService) GetChatModels() []ModelInfo {
    var chatModels []ModelInfo
    d.mutex.RLock()
    defer d.mutex.RUnlock()

    for _, endpoint := range d.endpoints {
        endpoint.mutex.RLock()
        for _, model := range endpoint.Models {
            if model.IsChat {
                chatModels = append(chatModels, model)
            }
        }
        endpoint.mutex.RUnlock()
    }
    return chatModels
}

// GetEmbeddingModels returns all models that support embeddings
func (d *DiscoveryService) GetEmbeddingModels() []ModelInfo {
    var embeddingModels []ModelInfo
    d.mutex.RLock()
    defer d.mutex.RUnlock()

    for _, endpoint := range d.endpoints {
        endpoint.mutex.RLock()
        for _, model := range endpoint.Models {
            if model.IsEmbedding {
                embeddingModels = append(embeddingModels, model)
            }
        }
        endpoint.mutex.RUnlock()
    }
    return embeddingModels
}

// GetAllEndpoints returns status of all endpoints
func (d *DiscoveryService) GetAllEndpoints() map[string]*LLMEndpoint {
    d.mutex.RLock()
    defer d.mutex.RUnlock()

    // Return a copy to avoid concurrent access issues
    result := make(map[string]*LLMEndpoint)
    for k, v := range d.endpoints {
        v.mutex.RLock()
        result[k] = &LLMEndpoint{
            BaseURL:     v.BaseURL,
            Models:      append([]ModelInfo{}, v.Models...),
            LastUpdated: v.LastUpdated,
            IsOnline:    v.IsOnline,
            ErrorCount:  v.ErrorCount,
        }
        v.mutex.RUnlock()
    }
    return result
}

// GetFirstModelName returns name of the first available model at the given URL
func (d *DiscoveryService) GetFirstModelName(baseURL string) (string, error) {
    d.mutex.RLock()
    defer d.mutex.RUnlock()

    endpoint, exists := d.endpoints[baseURL]
    if !exists {
        return "", fmt.Errorf("endpoint not found: %s", baseURL)
    }

    endpoint.mutex.RLock()
    defer endpoint.mutex.RUnlock()

    if len(endpoint.Models) == 0 {
        return "", fmt.Errorf("no models found at endpoint %s", baseURL)
    }

    return endpoint.Models[0].Name, nil
}
