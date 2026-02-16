// internal/goal/tree_builder.go
package goal

import (
    "context"
    "fmt"
    "log"
    "strings"
)

// TreeBuilder decomposes goals into hierarchical sub-goals.
type TreeBuilder struct {
    llm LLMService
}

// NewTreeBuilder creates a new tree builder.
func NewTreeBuilder(llm LLMService) *TreeBuilder {
    return &TreeBuilder{llm: llm}
}

// DecomposeGoal generates a sub-goal tree for the given goal.
func (t *TreeBuilder) DecomposeGoal(ctx context.Context, g *Goal) error {
    if len(g.SubGoals) > 0 {
        log.Printf("[TreeBuilder] Goal %s already has sub-goals, skipping.", g.ID)
        return nil
    }

    log.Printf("[TreeBuilder] Decomposing goal: %s", g.Description)

    prompt := fmt.Sprintf(`You are a strategic planner AI. Decompose the following goal into a hierarchy of actionable sub-goals.

Goal: %s
Type: %s

Instructions:
1. Break the goal into 3-5 high-level steps (Secondary Goals).
2. If a step is complex, break it into 2-3 detailed tasks (Tertiary Goals).
3. Assign a hierarchical ID to each (1, 1.1, 1.1.1).
4. Estimate effort for each: SIMPLE, MEDIUM, COMPLEX.
5. **Crucial**: Determine the best Action Type for each step:
   - RESEARCH: Needs web search or information retrieval.
   - PRACTICE: Requires simulation or self-training.
   - EXECUTE_TOOL: Needs a specific tool execution (specify tool name and parameters).
   - REFLECT: Needs internal analysis.
   - CREATE: Needs generating content or code.
6. **Parameters**: If action is EXECUTE_TOOL, provide a "params" object with the specific arguments needed (e.g., {"url": "http://example.com"}).

Output JSON format:
{
  "plan": [
    {
      "id": "1",
      "title": "...",
      "description": "...",
      "effort": "MEDIUM",
      "action_type": "RESEARCH",
      "tool_name": "search",
      "dependencies": [],
      "sub_steps": [
        {
          "id": "1.1",
          "title": "...",
          "description": "...",
          "effort": "SIMPLE",
          "action_type": "EXECUTE_TOOL",
          "tool_name": "browser",
          "dependencies": ["1"]
        }
      ]
    }
  ]
}`, g.Description, g.Type)

    var response struct {
        Plan []struct {
            ID          string                 `json:"id"`
            Title       string                 `json:"title"`
            Description string                 `json:"description"`
            Effort      string                 `json:"effort"`
            ActionType  string                 `json:"action_type"`
            ToolName    string                 `json:"tool_name"`
            Params      map[string]interface{} `json:"params"`
            Dependencies []string               `json:"dependencies"`
            SubSteps    []struct {
                ID          string                 `json:"id"`
                Title       string                 `json:"title"`
                Description string                 `json:"description"`
                Effort      string                 `json:"effort"`
                ActionType  string                 `json:"action_type"`
                ToolName    string                 `json:"tool_name"`
                Params      map[string]interface{} `json:"params"`
                Dependencies []string               `json:"dependencies"`
            } `json:"sub_steps"`
        } `json:"plan"`
    }

    if err := t.llm.GenerateJSON(ctx, prompt, &response); err != nil {
        return fmt.Errorf("failed to decompose goal: %w", err)
    }

    // Flatten hierarchy into SubGoal list
    var subGoals []SubGoal
    for _, step := range response.Plan {
        sg := SubGoal{
            ID:              step.ID,
            Title:           step.Title,
            Description:     step.Description,
            Status:          SubGoalPending,
            Dependencies:    step.Dependencies,
            EstimatedEffort: step.Effort,
            ActionType:      ActionType(step.ActionType),
            ToolName:        step.ToolName,
            Params:          step.Params,
        }
        subGoals = append(subGoals, sg)

        for _, sub := range step.SubSteps {
            ssg := SubGoal{
                ID:              sub.ID,
                Title:           sub.Title,
                Description:     sub.Description,
                Status:          SubGoalPending,
                Dependencies:    sub.Dependencies,
                EstimatedEffort: sub.Effort,
                ActionType:      ActionType(sub.ActionType),
                ToolName:        sub.ToolName,
                Params:          sub.Params,
            }
            subGoals = append(subGoals, ssg)
        }
    }

    g.SubGoals = subGoals
    g.TreeDepth = calculateDepth(subGoals)
    
    log.Printf("[TreeBuilder] Created tree with %d sub-goals for goal %s", len(subGoals), g.ID)
    return nil
}

func calculateDepth(sgs []SubGoal) int {
    maxDepth := 0
    for _, sg := range sgs {
        depth := strings.Count(sg.ID, ".") + 1
        if depth > maxDepth {
            maxDepth = depth
        }
    }
    return maxDepth
}

// ReplanSubTree regenerates a specific branch of the goal tree after failure.
// MDD 5.1 and Roadmap Step 13.
func (t *TreeBuilder) ReplanSubTree(ctx context.Context, g *Goal, failedSubGoalID string, failureReason string) error {
    log.Printf("[TreeBuilder] Replanning branch starting at %s due to: %s", failedSubGoalID, failureReason)

    // 1. Identify failed branch
    // We need to mark the specific sub-goal as failed (if not already) and generate alternatives.
    // For simplicity in this implementation, we will ask the LLM to generate NEW sub-goals 
    // given the failure context.

    prompt := fmt.Sprintf(`You are a strategic planner AI. A plan failed and needs adjustment.

Original Goal: %s
Failed Step: %s
Failure Reason: %s

Instructions:
1. Analyze the failure.
2. Propose 2-3 alternative sub-goals to bypass the failure.
3. Output JSON format with the new sub-goals (similar to decomposition logic).

Output JSON format:
{
  "new_plan": [
    {
      "id": "%s_alt1",
      "title": "...",
      "description": "...",
      "effort": "MEDIUM",
      "action_type": "RESEARCH"
    }
  ]
}`, g.Description, failedSubGoalID, failureReason, failedSubGoalID)

    var response struct {
        NewPlan []struct {
            ID          string `json:"id"`
            Title       string `json:"title"`
            Description string `json:"description"`
            Effort      string `json:"effort"`
            ActionType  string `json:"action_type"`
            ToolName    string `json:"tool_name"`
        } `json:"new_plan"`
    }

    if err := t.llm.GenerateJSON(ctx, prompt, &response); err != nil {
        return fmt.Errorf("failed to replan sub-tree: %w", err)
    }

    // 2. Remove the failed branch (optional, or just mark failed) and append new branches.
    // Here we append the new alternatives to the main list, marking them as alternatives.
    for _, np := range response.NewPlan {
        newSG := SubGoal{
            ID:              np.ID,
            Title:           np.Title,
            Description:     np.Description,
            Status:          SubGoalPending,
            EstimatedEffort: np.Effort,
            ActionType:      ActionType(np.ActionType),
            ToolName:        np.ToolName,
            Dependencies:    []string{failedSubGoalID}, // Dependency on the failed step implies "try after failure"
        }
        g.SubGoals = append(g.SubGoals, newSG)
    }

    // Mark original failed sub-goal as FAILED if not already
    for i, sg := range g.SubGoals {
        if sg.ID == failedSubGoalID {
            g.SubGoals[i].Status = SubGoalFailed
            g.SubGoals[i].FailureReason = failureReason
        }
    }

    log.Printf("[TreeBuilder] Added %d alternative steps for failed branch %s", len(response.NewPlan), failedSubGoalID)
    return nil
}
