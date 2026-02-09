package utils

import (
    "fmt"
    "regexp"
    "strconv"
    "strings"
)

// ParseOutcome extracts outcome, confidence, and reasoning from an S-Expression string.
// It implements "clever" recovery: if structured parsing fails for confidence, it scans the text.
func ParseOutcome(input string) (outcome string, confidence float64, reason string, err error) {
    // 1. Clean and Pre-process
    cleaned := cleanSExprInput(input)

    // 2. Extract Outcome (Simple Atom Search)
    // Look for (outcome "good") or (outcome good)
    outcome = extractFieldValue(cleaned, "outcome")
    if outcome == "" {
        return "", 0.0, "", fmt.Errorf("could not find 'outcome' field")
    }

    // 3. Extract Reason
    reason = extractFieldValue(cleaned, "reason")

    // 4. Extract Confidence (Clever Logic)
    confidence, err = extractConfidence(cleaned)
    if err != nil {
        return "", 0.0, "", err
    }

    return outcome, confidence, reason, nil
}

// ParseConcepts extracts a flat list of concepts from an S-Expression string.
func ParseConcepts(input string) ([]string, error) {
    cleaned := cleanSExprInput(input)

    // Look for (concepts "tag1" "tag2") or (tags "tag1" "tag2")
    // We try to find the content block.
    
    var concepts []string

    // Try to find the specific list block
    block := findBlock(cleaned, "concepts")
    if block == "" {
        block = findBlock(cleaned, "tags")
    }

    if block == "" {
        // Fallback: if no block wrapper found, maybe the whole input is just the list?
        // But for safety, let's assume the wrapper is expected.
        return nil, fmt.Errorf("could not find concepts or tags block")
    }

    // Now extract strings from the block content
    // Remove the wrapper identifier
    content := strings.TrimPrefix(block, "(concepts")
    content = strings.TrimPrefix(content, "(tags")
    content = strings.Trim(content, "()")
    
    // Tokenize simply by quotes for this flat list
    re := regexp.MustCompile(`"([^"]+)"`)
    matches := re.FindAllStringSubmatch(content, -1)

    for _, match := range matches {
        if len(match) > 1 {
            concepts = append(concepts, match[1])
        }
    }

    if len(concepts) == 0 {
        // Fallback: split by spaces if quotes failed (lenient)
        rawParts := strings.Fields(content)
        for _, part := range rawParts {
            cleanPart := strings.Trim(part, `"()`)
            if cleanPart != "" {
                concepts = append(concepts, cleanPart)
            }
        }
    }

    return concepts, nil
}

// --- Helper Functions ---

func cleanSExprInput(input string) string {
    // 1. Strip Markdown
    input = strings.TrimSpace(input)
    input = strings.TrimPrefix(input, "```lisp")
    input = strings.TrimPrefix(input, "```json")
    input = strings.TrimPrefix(input, "```")
    input = strings.TrimSuffix(input, "```")

    // 2. Fix Global Quotes (e.g. "(...)" -> (...))
    if len(input) >= 2 && strings.HasPrefix(input, "\"") && strings.HasSuffix(input, "\"") {
        if strings.Contains(input, "(") {
            input = input[1 : len(input)-1]
        }
    }
    
    // 3. Fix Malformed Quoted S-Expr (reasoning "(...)") -> (reasoning (...))
    input = strings.ReplaceAll(input, " \"(", " (")
    input = strings.ReplaceAll(input, ")\"", ")")

    // 4. Auto Balance Parens
    input = autoBalanceParens(input)

    return input
}

func autoBalanceParens(input string) string {
    depth := 0
    for _, ch := range input {
        if ch == '(' {
            depth++
        } else if ch == ')' {
            if depth > 0 {
                depth--
            }
        }
    }
    if depth > 0 {
        input += strings.Repeat(")", depth)
    }
    return input
}

func extractFieldValue(input, fieldName string) string {
    // Pattern: (fieldName "value") or (fieldName value)
    pattern := "(" + fieldName + " "
    start := strings.Index(input, pattern)
    if start == -1 {
        return ""
    }
    
    start += len(pattern)
    
    // Check if quoted
    if start < len(input) && input[start] == '"' {
        end := strings.Index(input[start+1:], "\"")
        if end == -1 {
            return "" // Malformed
        }
        return input[start+1 : start+1+end]
    }
    
    // Unquoted: read until space or )
    end := strings.IndexAny(input[start:], " )")
    if end == -1 {
        return input[start:]
    }
    return input[start : start+end]
}

func findBlock(input, blockName string) string {
    // Find (blockName ...)
    pattern := "(" + blockName + " "
    start := strings.Index(input, pattern)
    if start == -1 {
        return ""
    }

    // Find matching close paren
    depth := 0
    end := -1
    for i := start; i < len(input); i++ {
        if input[i] == '(' {
            depth++
        } else if input[i] == ')' {
            depth--
            if depth == 0 {
                end = i
                break
            }
        }
    }

    if end != -1 {
        return input[start : end+1]
    }
    return ""
}

func extractConfidence(input string) (float64, error) {
    // 1. Try to parse structured value (confidence 0.8)
    // This is handled by extractFieldValue, but we need to ensure it's a float
    rawVal := extractFieldValue(input, "confidence")
    
    // 2. Clever Recovery: Regex scan if structured value is missing or invalid
    // Pattern: "confidence" followed by whitespace and a number (e.g. 0.85)
    re := regexp.MustCompile(`(?i)confidence\s+([0-9]*\.?[0-9]+)`)
    matches := re.FindStringSubmatch(input)
    
    var valStr string
    if matches != nil && len(matches) > 1 {
        valStr = matches[1]
    } else if rawVal != "" {
        valStr = rawVal
    }

    if valStr == "" {
        return 0.0, fmt.Errorf("confidence value not found")
    }

    val, err := strconv.ParseFloat(valStr, 64)
    if err != nil {
        return 0.0, fmt.Errorf("confidence value '%s' is not a number", valStr)
    }

    // Strict Validation: 0.00 to 1.00
    if val < 0.0 || val > 1.0 {
        return 0.0, fmt.Errorf("confidence value %.2f is out of range [0.0, 1.0]", val)
    }

    return val, nil
}
