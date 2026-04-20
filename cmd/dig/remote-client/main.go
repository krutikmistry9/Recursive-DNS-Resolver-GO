package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"time"
)

type ResolveResult struct {
	Domain  string `json:"Domain"`
	Type    string `json:"Type"`
	Cached  bool   `json:"Cached"`
	Error   string `json:"Error"`
	Latency int64  `json:"Latency"` // nanoseconds
	Records []struct {
		Type  string `json:"Type"`
		Value string `json:"Value"`
		TTL   int    `json:"TTL"`
	} `json:"Records"`
}

func main() {
	server := flag.String("server", "http://192.168.1.10:8053", "resolver base URL (Device A)")
	domain := flag.String("domain", "google.com", "domain to resolve")
	qtype := flag.String("type", "A", "record type (A, AAAA, MX, TXT, ...)")
	flag.Parse()

	u, _ := url.Parse(*server)
	u.Path = "/resolve"
	q := u.Query()
	q.Set("domain", *domain)
	q.Set("type", *qtype)
	u.RawQuery = q.Encode()

	client := &http.Client{Timeout: 5 * time.Second}

	resp, err := client.Get(u.String())
	if err != nil {
		log.Fatalf("request failed: %v", err)
	}
	defer resp.Body.Close()

	var result ResolveResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		log.Fatalf("decode failed: %v", err)
	}

	if result.Error != "" {
		log.Fatalf("resolver error: %s", result.Error)
	}

	fmt.Printf("Domain: %s (%s)\n", result.Domain, result.Type)
	fmt.Printf("Cached: %v\n", result.Cached)
	fmt.Printf("Latency: %s\n", time.Duration(result.Latency))
	fmt.Println("Records:")
	for _, r := range result.Records {
		fmt.Printf("  %-5s %-40s TTL=%ds\n", r.Type, r.Value, r.TTL)
	}
}