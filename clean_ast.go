package main

import (
    "bytes"
    "fmt"
    "go/ast"
    "go/parser"
    "go/printer"
    "go/token"
    "os"
)

// Functions to remove
var targets = map[string]bool{
    "generateResearchPlan":         true,
    "getNextResearchAction":       true,
    "updateResearchProgress":      true,
    "synthesizeResearchFindings":  true,
    "storeResearchSynthesis":      true,
    "executeAction":              true,
    "getPrimaryGoals":            true,
    "validateGoalSupport":        true,
    "parseGoalSupportValidation":  true,
    "parseActionFromPlan":        true,
    "validateToolExists":         true,
    "getAvailableToolsList":      true,
    "parseAssessmentSExpr":      true,
    "replanGoal":               true,
    "evaluatePrincipleEffectiveness": true,
    "createSelfModificationGoal": true,
    "testPrincipleModification":  true,
}

func main() {
    if len(os.Args) < 2 {
        fmt.Println("Usage: go run clean_ast.go <filename>")
        os.Exit(1)
    }

    filename := os.Args[1]

    fset := token.NewFileSet()
    node, err := parser.ParseFile(fset, filename, nil, parser.ParseComments)
    if err != nil {
        panic(err)
    }

    // Filter the declarations
    var filtered []ast.Decl
    for _, decl := range node.Decls {
        if fd, ok := decl.(*ast.FuncDecl); ok {
            if targets[fd.Name.Name] {
                // Skip this function (delete it)
                continue
            }
        }
        filtered = append(filtered, decl)
    }

    node.Decls = filtered

    // Print the cleaned file
    var buf bytes.Buffer
    if err := printer.Fprint(&buf, fset, node); err != nil {
        panic(err)
    }

    fmt.Print(buf.String())
}
