package main

import (
    "bufio"
    "fmt"
    "os"
    "strings"
)

// List of functions to remove
var functionsToRemove = []string{
    "(e *Engine) generateResearchPlan",
    "(e *Engine) getNextResearchAction",
    "(e *Engine) updateResearchProgress",
    "(e *Engine) synthesizeResearchFindings",
    "(e *Engine) storeResearchSynthesis",
    "(e *Engine) executeAction",
    "(e *Engine) getPrimaryGoals",
    "(e *Engine) validateGoalSupport",
    "(e *Engine) parseGoalSupportValidation",
    "(e *Engine) parseActionFromPlan",
    "(e *Engine) validateToolExists",
    "(e *Engine) getAvailableToolsList",
    "(e *Engine) parseAssessmentSExpr",
    "(e *Engine) replanGoal",
    "(e *Engine) evaluatePrincipleEffectiveness",
    "(e *Engine) createSelfModificationGoal",
    "(e *Engine) testPrincipleModification",
}

func main() {
    if len(os.Args) < 2 {
        fmt.Println("Usage: go run cleanup.go <file_to_clean>")
        os.Exit(1)
    }

    filename := os.Args[1]
    inputFile, err := os.Open(filename)
    if err != nil {
        panic(err)
    }
    defer inputFile.Close()

    outputFile, err := os.Create(filename + "_cleaned")
    if err != nil {
        panic(err)
    }
    defer outputFile.Close()

    scanner := bufio.NewScanner(inputFile)
    writer := bufio.NewWriter(outputFile)
    defer writer.Flush()

    skip := false
    braceDepth := 0
    currentFunc := ""

    for scanner.Scan() {
        line := scanner.Text()
        trimmed := strings.TrimSpace(line)

        // Check if this line starts a function we want to remove
        if strings.HasPrefix(trimmed, "func ") {
            for _, target := range functionsToRemove {
                if strings.Contains(trimmed, target) {
                    skip = true
                    currentFunc = target
                    braceDepth = 0
                    // Reset depth based on opening brace on this line
                    braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
                    break
                }
            }
        }

        if skip {
            // Track braces to find the end of the function
            braceDepth += strings.Count(line, "{") - strings.Count(line, "}")
            
            // If we return to 0, we found the end
            if braceDepth == 0 {
                skip = false
                currentFunc = ""
            }
            // Do not write this line to the new file
            continue
        }

        // Write the line if we aren't skipping
        writer.WriteString(line + "\n")
    }

    fmt.Println("Cleaned file written to " + filename + "_cleaned")
    fmt.Println("Please verify and then rename it to replace the original.")
}
