// internal/goal/metrics.go
package goal

import (
    "context"
    "fmt"
    "log"
)

// MetricDerivationEngine defines success criteria for goals.
type MetricDerivationEngine struct {
    llm LLMService
}

// NewMetricDerivationEngine creates a new metric engine.
func NewMetricDerivationEngine(llm LLMService) *MetricDerivationEngine {
    return &MetricDerivationEngine{llm: llm}
}

// DeriveMetrics generates success criteria for a goal.
func (m *MetricDerivationEngine) DeriveMetrics(ctx context.Context, g *Goal) error {
    if g.SuccessCriteria != "" {
        return nil // Already defined
    }

    log.Printf("[Metrics] Deriving success metrics for goal: %s", g.Description)

    prompt := fmt.Sprintf(`Define success criteria and measurement methods for the following goal.

Goal: %s
Type: %s

Output JSON format:
{
  "success_criteria": "Clear definition of 'done'",
  "measurement_method": "How to measure progress (e.g., test score, output verification)",
  "completion_threshold": 0.8, // 0.0 to 1.0 float
  "metrics": {
    "key": "value" // Relevant initial metric values
  }
}`, g.Description, g.Type)

    var response struct {
        SuccessCriteria     string                 `json:"success_criteria"`
        MeasurementMethod   string                 `json:"measurement_method"`
        CompletionThreshold float64                `json:"completion_threshold"`
        Metrics             map[string]interface{} `json:"metrics"`
    }

    if err := m.llm.GenerateJSON(ctx, prompt, &response); err != nil {
        return fmt.Errorf("failed to derive metrics: %w", err)
    }

    g.SuccessCriteria = response.SuccessCriteria
    g.MeasurementMethod = response.MeasurementMethod
    g.CompletionThreshold = response.CompletionThreshold
    if g.CompletionThreshold == 0 {
        g.CompletionThreshold = 1.0 // Default to 100%
    }
    g.CurrentMetricValues = response.Metrics

    log.Printf("[Metrics] Defined metrics for goal %s: %s", g.ID, g.SuccessCriteria)
    return nil
}
