package main

import (
    "fmt"
    "go-llama/internal/api"
)

func main() {
    url := "https://simple.wikipedia.org/wiki/List_of_prime_ministers_of_the_United_Kingdom"
    fallback := "(fallback snippet if fetch fails)"
    summary := api.EnrichAndSummarize(url, fallback)
    fmt.Println("\n=== Condensed Summary ===")
    fmt.Println(summary)
}
