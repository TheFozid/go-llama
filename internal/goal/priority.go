package goal

import (
    "math"
	"math/rand"
)

// Calculator handles priority and scoring logic
type Calculator struct {
    config *PriorityConfig
}

// NewCalculator creates a new priority calculator
func NewCalculator(config *PriorityConfig) *Calculator {
    if config == nil {
        config = DefaultPriorityConfig()
    }
    return &Calculator{config: config}
}

// ApplyDecay reduces priority based on goal state
func (c *Calculator) ApplyDecay(g *Goal, cyclesElapsed int) {
    var decayAmount float64
    if g.State == StateActive {
        decayAmount = c.config.DecayRateActive * float64(cyclesElapsed)
    } else if g.State == StateQueued {
        decayAmount = c.config.DecayRateQueued * float64(cyclesElapsed)
    } else {
        // Paused or other states do not decay
        return
    }

    newPriority := float64(g.CurrentPriority) - decayAmount
    if newPriority < 10 {
        newPriority = 10 // Floor for decay before archival
    }
    g.CurrentPriority = int(newPriority)
}

// ApplyStrengthening increases priority due to repeated proposals
func (c *Calculator) ApplyStrengthening(g *Goal) {
    // Strengthening magnitude between min and max
    strength := c.config.StrengtheningMin
    if c.config.StrengtheningMax > c.config.StrengtheningMin {
        strength += rand.Intn(c.config.StrengtheningMax - c.config.StrengtheningMin)
    }

    newPriority := g.CurrentPriority + strength
    
    // Cap at PriorityCap (100)
    if newPriority > g.PriorityCap {
        newPriority = g.PriorityCap
    }
    
    g.CurrentPriority = newPriority
    g.ProposalCount++
}

// CalculateSelectionScore calculates the score used to rank goals for selection
// Formula: Priority / (TimeScore ^ Exponent)
func (c *Calculator) CalculateSelectionScore(g *Goal) float64 {
    if g.TimeScore == 0 {
        // Avoid division by zero; if effort is unknown, assume 1
        return float64(g.CurrentPriority)
    }

    ts := float64(g.TimeScore)
    p := float64(g.CurrentPriority)
    exp := c.config.SelectionExponent

    return p / math.Pow(ts, exp)
}

// CalculateProgressBonus adjusts the score for an active goal during review
func (c *Calculator) CalculateProgressBonus(g *Goal) float64 {
    baseScore := c.CalculateSelectionScore(g)
    progressBonus := 1.0 + (g.ProgressPercentage * c.config.ProgressBonusFactor)
    return baseScore * progressBonus
}

// ShouldSwitchGoal determines if a new proposed goal should interrupt the active goal
func (c *Calculator) ShouldSwitchGoal(activeGoal *Goal, proposedGoal *Goal) bool {
    // 1. Calculate raw scores
    activeScore := c.CalculateProgressBonus(activeGoal)
    proposedScore := c.CalculateSelectionScore(proposedGoal)

    // 2. Check completion protection
    if activeGoal.ProgressPercentage >= 80.0 {
        // Never switch if active goal is nearly complete
        return false
    }

    // 3. Determine margin required
    margin := 0.0
    if proposedGoal.Origin == OriginUser {
        margin = 10.0
    } else {
        margin = 30.0 // AI goals need significantly higher priority
    }

    // 4. Compare scores
    return proposedScore > (activeScore + margin)
}
