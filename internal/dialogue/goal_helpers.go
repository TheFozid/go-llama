package dialogue

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"
)

// proposeSecondaryGoal uses LLM to suggest a secondary goal that supports a primary
func (e *Engine) proposeSecondaryGoal(ctx context.Context, primaryGoals []Goal, existingSecondaries []Goal) (*SecondaryGoalProposal, error) {
	// Build context
	var primaryContext strings.Builder
	primaryContext.WriteString("ACTIVE PRIMARY GOALS:\n")
	for i, goal := range primaryGoals {
		primaryContext.WriteString(fmt.Sprintf("%d. [ID: %s] %s (progress: %.0f%%)\n",
			i+1, goal.ID, goal.Description, goal.Progress*100))
	}

	var secondaryContext strings.Builder
	if len(existingSecondaries) > 0 {
		secondaryContext.WriteString("\nEXISTING SECONDARY GOALS:\n")
		for i, goal := range existingSecondaries {
			primaryID := "none"
			if len(goal.SupportsGoals) > 0 {
				primaryID = goal.SupportsGoals[0]
			}
			secondaryContext.WriteString(fmt.Sprintf("%d. %s (supports: %s, progress: %.0f%%)\n",
				i+1, goal.Description, truncate(primaryID, 20), goal.Progress*100))
		}
	}

	prompt := fmt.Sprintf(`%s
%s

Propose ONE secondary goal that would help advance a primary goal.

REQUIREMENTS:
1. Must directly support one of the primary goals listed above
2. Should be specific and actionable
3. Should NOT duplicate existing secondaries
4. Should be achievable through research/learning
5. Priority 6-8 (secondaries are supporting, not urgent)

Respond ONLY with valid JSON:
{
  "description": "Research specific aspect of primary goal",
  "priority": 7,
  "reasoning": "This will help primary goal by providing X",
  "expected_time": "3-5 cycles",
  "action_plan": ["Search for Y", "Parse detailed article", "Synthesize findings"],
  "supports_goal_id": "goal_123"
}

If NO secondary goal is needed (primaries already well-supported), respond:
{
  "description": "",
  "priority": 0,
  "reasoning": "Existing secondaries adequately support all primaries",
  "expected_time": "",
  "action_plan": [],
  "supports_goal_id": ""
}`, primaryContext.String(), secondaryContext.String())

	response, tokens, err := e.callLLMWithStructuredReasoning(ctx, prompt, false)
	if err != nil {
		return nil, fmt.Errorf("LLM proposal failed: %w", err)
	}

	log.Printf("[GoalProposal] Secondary goal proposal completed (%d tokens)", tokens)

	// Parse response
	content := strings.TrimSpace(response.RawResponse)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimPrefix(content, "```")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var proposal SecondaryGoalProposal
	if err := json.Unmarshal([]byte(content), &proposal); err != nil {
		return nil, fmt.Errorf("failed to parse proposal: %w", err)
	}

	// Check if LLM declined (empty description)
	if proposal.Description == "" {
		return nil, nil // No goal needed
	}

	// Validate
	if proposal.Priority < 1 || proposal.Priority > 10 {
		proposal.Priority = 7 // Default
	}

	if proposal.SupportsGoalID == "" {
		return nil, fmt.Errorf("proposal missing supports_goal_id")
	}

	// Verify the primary goal ID exists
	found := false
	for _, primary := range primaryGoals {
		if primary.ID == proposal.SupportsGoalID {
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("supports_goal_id '%s' not found in primary goals", proposal.SupportsGoalID)
	}

	return &proposal, nil
}

// getPrimaryGoals filters goals by primary tier
func getPrimaryGoals(goals []Goal) []Goal {
	primaries := []Goal{}
	for _, goal := range goals {
		if goal.Tier == "primary" {
			primaries = append(primaries, goal)
		}
	}
	return primaries
}

// getSecondaryGoals filters goals by secondary tier
func getSecondaryGoals(goals []Goal) []Goal {
	secondaries := []Goal{}
	for _, goal := range goals {
		if goal.Tier == "secondary" {
			secondaries = append(secondaries, goal)
		}
	}
	return secondaries
}

// checkAllSupportingGoalsComplete checks if all secondaries supporting a primary are complete
func checkAllSupportingGoalsComplete(primaryGoal *Goal, state *InternalState) bool {
	// Find all secondaries that support this primary
	supportingSecondaries := []Goal{}
	for _, goal := range state.ActiveGoals {
		if goal.Tier == "secondary" && len(goal.SupportsGoals) > 0 {
			for _, supportsID := range goal.SupportsGoals {
				if supportsID == primaryGoal.ID {
					supportingSecondaries = append(supportingSecondaries, goal)
					break
				}
			}
		}
	}

	// If no secondaries support this primary, it can't be complete yet
	if len(supportingSecondaries) == 0 {
		return false
	}

	// Check if all supporting secondaries are complete
	for _, secondary := range supportingSecondaries {
		if secondary.Status != GoalStatusCompleted {
			return false
		}
	}

	return true
}

// findPrimaryGoal finds a primary goal by ID in state
func findPrimaryGoal(goalID string, state *InternalState) *Goal {
	for i := range state.ActiveGoals {
		if state.ActiveGoals[i].ID == goalID && state.ActiveGoals[i].Tier == "primary" {
			return &state.ActiveGoals[i]
		}
	}
	return nil
}

// createSecondaryGoalFromProposal converts a proposal into an actual Goal
func (e *Engine) createSecondaryGoalFromProposal(proposal *SecondaryGoalProposal) Goal {
	return Goal{
		ID:            fmt.Sprintf("goal_%d", time.Now().UnixNano()),
		Description:   proposal.Description,
		Source:        GoalSourceKnowledgeGap,
		Priority:      proposal.Priority,
		Created:       time.Now(),
		Progress:      0.0,
		Status:        GoalStatusActive,
		Actions:       []Action{},
		Tier:          "secondary",
		SupportsGoals: []string{proposal.SupportsGoalID},
		DependencyScore: 0.8, // High confidence since LLM proposed it
		HasPendingWork: false,
		LastPursued:    time.Time{},
	}
}
