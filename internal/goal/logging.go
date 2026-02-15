package goal

import (
    "log"
    "time"
)

// GoalSystemLogger provides structured logging for the autonomous goal system.
// It wraps the standard log package to provide consistent, parseable output.
type GoalSystemLogger struct{}

// NewGoalSystemLogger creates a new logger instance.
func NewGoalSystemLogger() *GoalSystemLogger {
    return &GoalSystemLogger{}
}

// log is an internal helper to format output consistently.
func (l *GoalSystemLogger) log(level, category, format string, args ...interface{}) {
    prefix := "[GoalSystem][" + level + "][" + category + "] "
    log.Printf(prefix+format, args...)
}

// LogStateTransition logs a change in goal state.
// Satisfies Roadmap Step 23: "logStateTransition(goal, fromState, toState, reason)"
func (l *GoalSystemLogger) LogStateTransition(goalID string, from, to GoalState, reason string) {
    l.log("INFO", "STATE", "Goal %s transitioned: %s -> %s | Reason: %s", goalID, from, to, reason)
}

// LogPriorityChange logs a modification to a goal's priority.
// Satisfies Roadmap Step 23: "logPriorityChange(goal, oldPriority, newPriority, reason)"
func (l *GoalSystemLogger) LogPriorityChange(goalID string, oldPriority, newPriority int, reason string) {
    l.log("INFO", "PRIORITY", "Goal %s priority: %d -> %d | Reason: %s", goalID, oldPriority, newPriority, reason)
}

// LogGoalDecision logs significant decision-making events.
// Satisfies Roadmap Step 23: "logGoalDecision(decision, reasoning, alternatives)"
func (l *GoalSystemLogger) LogGoalDecision(decision string, reasoning string, alternatives []string) {
    l.log("INFO", "DECISION", "Decision: %s | Reasoning: %s | Alternatives: %v", decision, reasoning, alternatives)
}

// LogSubGoalExecution logs the execution details of a sub-goal.
// Satisfies Roadmap Step 23: "logSubGoalExecution(subGoal, result, duration)"
func (l *GoalSystemLogger) LogSubGoalExecution(subGoalID string, result string, duration time.Duration) {
    l.log("DEBUG", "EXECUTION", "SubGoal %s executed | Duration: %s | Result: %s", subGoalID, duration, result)
}

// LogReviewOutcome logs the result of a goal review cycle.
// Satisfies Roadmap Step 23: "logReviewOutcome(goal, outcome, reasoning)"
func (l *GoalSystemLogger) LogReviewOutcome(goalID string, outcome string, reasoning string) {
    l.log("INFO", "REVIEW", "Goal %s review finished | Outcome: %s | Reasoning: %s", goalID, outcome, reasoning)
}

// LogSkillAcquisition logs when a new skill is registered.
// Satisfies Roadmap Step 23: "logSkillAcquisition(skill, goal, proficiency)"
func (l *GoalSystemLogger) LogSkillAcquisition(skillID string, goalID string, proficiency SkillProficiency) {
    l.log("INFO", "SKILL", "Skill Acquired: %s | Proficiency: %s | Context Goal: %s", skillID, proficiency, goalID)
}

// LogError logs errors with operational context.
// Satisfies Roadmap Step 23: "logError(operation, error, context)"
func (l *GoalSystemLogger) LogError(operation string, err error, context map[string]interface{}) {
    l.log("ERROR", "SYSTEM", "Operation '%s' failed | Error: %v | Context: %v", operation, err, context)
}
