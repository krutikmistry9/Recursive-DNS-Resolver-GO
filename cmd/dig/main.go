// cmd/dig/main.go — a minimal CLI tool for testing the resolver directly.
//
// Usage:
//   go run ./cmd/dig google.com A
//   go run ./cmd/dig github.com AAAA
//   go run ./cmd/dig mail.google.com MX
package main

import (
	"fmt"
	"log"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"dns-resolver/cache"
	"dns-resolver/resolver"
)

func main() {
	domain := "google.com"
	qtype := "A"

	if len(os.Args) >= 2 {
		domain = os.Args[1]
	}
	if len(os.Args) >= 3 {
		qtype = strings.ToUpper(os.Args[2])
	}

	logger := log.New(os.Stderr, "", log.LstdFlags)
	c := cache.New()
	res := resolver.New(c, logger, "info")

	fmt.Printf("\n🔍 Resolving %s (%s)...\n\n", domain, qtype)

	result := res.Resolve(domain, qtype)

	// Print resolution steps
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "STEP\tSTAGE\tSERVER\tRESPONSE\tDURATION")
	fmt.Fprintln(w, "----\t-----\t------\t--------\t--------")
	for i, step := range result.Steps {
		dur := ""
		if step.Duration > 0 {
			dur = step.Duration.Round(time.Millisecond).String()
		}
		server := step.Server
		if server == "" {
			server = "(local cache)"
		}
		fmt.Fprintf(w, "%d\t%s\t%s\t%s\t%s\n", i+1, step.Stage, server, truncate(step.Response, 60), dur)
	}
	w.Flush()

	fmt.Println()

	if result.Error != "" {
		fmt.Printf("❌ Error: %s\n", result.Error)
		os.Exit(1)
	}

	if result.Cached {
		fmt.Println("✅ Served from cache")
	}

	fmt.Printf("⏱  Total latency: %s\n\n", result.Latency.Round(time.Millisecond))
	fmt.Printf("📋 Records for %s (%s):\n", result.Domain, result.Type)

	w2 := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w2, "TYPE\tVALUE\tTTL")
	fmt.Fprintln(w2, "----\t-----\t---")
	for _, rec := range result.Records {
		fmt.Fprintf(w2, "%s\t%s\t%ds\n", rec.Type, rec.Value, rec.TTL)
	}
	w2.Flush()
	fmt.Println()

	// Second run to show cache hit
	fmt.Println("🔁 Running same query again (should be cached)...")
	result2 := res.Resolve(domain, qtype)
	if result2.Cached {
		fmt.Printf("✅ Cache HIT in %s\n", result2.Latency.Round(time.Microsecond))
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-3] + "..."
}
