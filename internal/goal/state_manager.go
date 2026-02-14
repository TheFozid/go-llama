package goal

import (
    "fmt"
    "sync"
    "time"
)

// TransitionListener is a callback function triggered on state changes
type TransitionListener func(goalID string, from, to GoalState, timestamp time.Time)

// StateManager handles valid state transitions for goals
type StateManager struct {
    mu         sync.RWMutex
    listeners  []TransitionListener
}

// NewStateManager creates a new state manager
func NewStateManager() *StateManager {
    return &StateManager{
        listeners: make([]TransitionListener, 0),
    }
}

// validTransitions defines the map of allowed state changes
// Key: FromState -> Value: Set of allowed ToStates
var validTransitions = map[GoalState]map[GoalState]bool{
    StateProposed: {
        StateValidating: true,
    },
    StateValidating: {
        StateQueued:   true,
        StateArchived: true,
    },
    StateQueued: {
        StateActive:   true,
        StateArchived: true, // Priority decay
    },
    StateActive: {
        StateReviewing: true,
        StatePaused:    true,
        StateCompleted: true,
        StateArchived:  true,
    },
    StateReviewing: {
        StateActive:    true,
        StateQueued:    true,
        StateCompleted: true,
        StateArchived:  true,
    },
    StatePaused: {
        StateQueued: true,
    },
    StateCompleted: {}, // Terminal state
    StateArchived: {
        StateQueued: true, // Revival process
    },
}

// CanTransition checks if a transition is valid
func (sm *StateManager) CanTransition(from, to GoalState) bool {
    sm.mu.RLock()
    defer sm.mu.RUnlock()

    if allowedStates, exists := validTransitions[from]; exists {
        return allowedStates[to]
    }
    return false
}

// Transition attempts to change the goal's state
func (sm *StateManager) Transition(g *Goal, toState GoalState) error {
    sm.mu.Lock()
    defer sm.mu.Unlock()

    fromState := g.State

    if !validTransitions[fromState][toState] {
        return fmt.Errorf("invalid state transition from %s to %s", fromState, toState)
    }

    // Perform the transition
    g.State = toState
    now := time.Now()

    // Specific side-effects
    if toState == StateActive {
        g.LastProgressTimestamp = now
    } else if toState == StateArchived {
        g.ArchiveTimestamp = now
    }

    // Notify listeners (async to prevent deadlock)
    for _, listener := range sm.listeners {
        go listener(g.ID, fromState, toState, now)
    }

    return nil
}

// GetValidTransitions returns all possible next states for a given state
func (sm *StateManager) GetValidTransitions(current GoalState) []GoalState {
    sm.mu.RLock()
    defer sm.mu.RUnlock()

    states := make([]GoalState, 0)
    if allowed, exists := validTransitions[current]; exists {
        for s := range allowed {
            states = append(states, s)
        }
    }
    return states
}

// AddListener registers a callback for state changes
func (sm *StateManager) AddListener(listener TransitionListener) {
    sm.mu.Lock()
    defer sm.mu.Unlock()
    sm.listeners = append(sm.listeners, listener)
}

// ValidateTransitionMap is a utility to print the transition map (for debugging)
func ValidateTransitionMap() {
    fmt.Println("GOAL STATE TRANSITION MAP")
    fmt.Println("========================")
    for from, toStates := range validTransitions {
        fmt.Printf("%s -> ", from)
        for to := range toStates {
            fmt.Printf("%s ", to)
        }
        fmt.Println()
    }
}
