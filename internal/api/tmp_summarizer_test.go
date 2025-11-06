package api

import (
	"fmt"
	"os"
	"testing"
)

// Run with:
//   TEST_URL="https://simple.wikipedia.org/wiki/List_of_prime_ministers_of_the_United_Kingdom" go test -run TestSummarizer -v ./internal/api
func TestSummarizer(t *testing.T) {
	url := os.Getenv("TEST_URL")
	if url == "" {
		url = "https://simple.wikipedia.org/wiki/List_of_prime_ministers_of_the_United_Kingdom"
	}
	s := enrichAndSummarize(url, "(fallback snippet)")
	fmt.Println("\n=== Condensed Summary ===")
	fmt.Println(s)
}
