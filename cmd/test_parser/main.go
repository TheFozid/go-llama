package main

import (
    "bytes"
    "context"
    "encoding/json"
    "fmt"
    "io"
    "log"
    "net/http"
    "os"
    "time"

    "go-llama/internal/config"
    "go-llama/internal/tools"
)

// directLLMClient is a minimal HTTP client to bypass the Queue Manager for testing
type directLLMClient struct{}

func (d *directLLMClient) Call(ctx context.Context, llmURL string, payload map[string]interface{}) ([]byte, error) {
    jsonPayload, err := json.Marshal(payload)
    if err != nil {
        return nil, err
    }

    req, err := http.NewRequestWithContext(ctx, "POST", llmURL, bytes.NewBuffer(jsonPayload))
    if err != nil {
        return nil, err
    }
    req.Header.Set("Content-Type", "application/json")

    client := &http.Client{Timeout: 120 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return nil, fmt.Errorf("LLM returned status %d", resp.StatusCode)
    }

    return io.ReadAll(resp.Body)
}

func main() {
    // 1. Check Args
    args := os.Args
    if len(args) < 2 {
        fmt.Println("Usage: go run cmd/test_parser/main.go <URL> [GOAL]")
        fmt.Println("Example: go run cmd/test_parser/main.go https://example.com")
        fmt.Println("Example (Selective): go run cmd/test_parser/main.go https://example.com \"find information about pricing\"")
        os.Exit(1)
    }

    targetURL := args[1]
    goal := ""
    if len(args) >= 3 {
        goal = args[2]
    }

    // 2. Load Config
    cfg, err := config.LoadConfig("config.json")
    if err != nil {
        log.Fatalf("Failed to load config.json: %v", err)
    }

    // 3. Setup Tool
    // Use a generous timeout for testing
    toolConfig := tools.ToolConfig{
        Enabled:     true,
        TimeoutIdle: 120, // 2 minutes
    }

    llmClient := &directLLMClient{}
    
    // Use the Reasoning Model URL and Name from config
    llmURL := config.GetChatURL(cfg.GrowerAI.ReasoningModel.URL)
    llmModel := cfg.GrowerAI.ReasoningModel.Name

    tool := tools.NewWebParserUnifiedTool(
        cfg.GrowerAI.Tools.WebParse.UserAgent,
        llmURL,
        llmModel,
        cfg.GrowerAI.Tools.WebParse.MaxPageSizeMB,
        toolConfig,
        llmClient, // Inject our direct client
    )

    fmt.Printf("Testing Web Parser...\n")
    fmt.Printf("URL: %s\n", targetURL)
    if goal != "" {
        fmt.Printf("GOAL: %s\n", goal)
    } else {
        fmt.Printf("GOAL: (None - Full Parse Mode)\n")
    }
    fmt.Println("---")

    // 4. Execute
    ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
    defer cancel()

    params := map[string]interface{}{
        "url":  targetURL,
        "goal": goal, // Can be empty string
    }

    result, err := tool.Execute(ctx, params)

    // 5. Output
    if err != nil {
        log.Fatalf("Tool Execution Failed: %v", err)
    }

    fmt.Printf("\n\n=== RESULT ===\n")
    fmt.Printf("Success: %v\n", result.Success)
    if !result.Success {
        fmt.Printf("Error: %s\n", result.Error)
        os.Exit(1)
    }

    fmt.Printf("Duration: %v\n", result.Duration)
    fmt.Printf("Tokens Used: %d\n", result.TokensUsed)
    
    if result.Metadata != nil {
        fmt.Printf("\n--- METADATA ---\n")
        for k, v := range result.Metadata {
            fmt.Printf("%s: %v\n", k, v)
        }
    }

    fmt.Printf("\n--- OUTPUT ---\n")
    fmt.Println(result.Output)
}
