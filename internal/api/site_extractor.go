package api

import (
	"regexp"
	"strings"
)

// extractSiteQuery detects patterns like "search www.bbc.co.uk for ..." or "look at cnn.com"
// and returns (cleanPrompt, siteDomain).
func extractSiteQuery(prompt string) (string, string) {
	re := regexp.MustCompile(`(?i)(search|look|find|check|browse|on|at|in|up)\s+(?:https?://)?(?:www\.)?([\w.-]+\.\w{2,})`)
	m := re.FindStringSubmatch(prompt)
	if len(m) < 3 {
		return prompt, ""
	}
	domain := strings.TrimSpace(m[2])
	clean := strings.Replace(prompt, m[0], "", 1)
	return strings.TrimSpace(clean), domain
}
