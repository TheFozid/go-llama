import sys

# Functions we want to remove
functions_to_remove = [
    "func (e *Engine) generateResearchPlan",
    "func (e *Engine) getNextResearchAction",
    "func (e *Engine) updateResearchProgress",
    "func (e *Engine) synthesizeResearchFindings",
    "func (e *Engine) storeResearchSynthesis",
    "func (e *Engine) executeAction",
    "func (e *Engine) getPrimaryGoals",
    "func (e *Engine) validateGoalSupport",
    "func (e *Engine) parseGoalSupportValidation",
    "func (e *Engine) parseActionFromPlan",
    "func (e *Engine) validateToolExists",
    "func (e *Engine) getAvailableToolsList",
    "func (e *Engine) parseAssessmentSExpr",
    "func (e *Engine) replanGoal",
    "func (e *Engine) evaluatePrincipleEffectiveness",
    "func (e *Engine) createSelfModificationGoal",
    "func (e *Engine) testPrincipleModification"
]

if len(sys.argv) < 2:
    print("Usage: python clean_engine.py <filename>")
    sys.exit(1)

filename = sys.argv[1]

with open(filename, 'r') as f:
    lines = f.readlines()

skip = False
depth = 0
output_lines = []

for line in lines:
    stripped = line.strip()
    
    # Check if this line starts a function we want to remove
    if any(func in stripped for func in functions_to_remove):
        skip = True
        depth = line.count('{') - line.count('}')
        continue
    
    if skip:
        # Count braces to find the end of the function
        depth += line.count('{') - line.count('}')
        if depth == 0:
            skip = False
        continue
    
    # If we aren't skipping, add the line
    output_lines.append(line)

# Write back
with open(filename, 'w') as f:
    f.writelines(output_lines)

print(f"Cleaned {filename}")
