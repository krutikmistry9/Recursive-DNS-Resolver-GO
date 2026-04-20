# Recursive DNS Resolver

**Course:** Computer Networks  
**Student:** Krutik Mistry  
**Language:** Go (Golang)

A fully custom recursive DNS resolver implementing the complete DNS hierarchy (Root → TLD → Authoritative), TTL-based caching, error handling, and a real-time streaming HTTP API — without relying on the OS resolver.

---

## Project Structure

```
dns-resolver/
├── main.go              # HTTP server entry point
├── go.mod
├── cmd/
│   └── dig/
│       └── main.go     # CLI test tool
├── dns/
│   └── dns.go          # Wire-format DNS packet encoder/decoder
├── cache/
│   └── cache.go        # Thread-safe TTL cache
├── resolver/
│   └── resolver.go     # Recursive resolution logic
└── server/
    └── server.go       # HTTP API
```

---

## Getting Started

### Prerequisites

- Go 1.21+

### Run the HTTP Server

```bash
go run main.go
# or with options:
go run main.go -http :8053 -log debug
```

### Use the CLI Tool

```bash
go run ./cmd/dig google.com A
go run ./cmd/dig github.com AAAA
go run ./cmd/dig mail.google.com MX
```

---

## HTTP API

All endpoints run on `http://localhost:8053` by default.

### `GET /resolve?domain=<domain>&type=<type>`

Full recursive resolution. Returns JSON.

```bash
curl "http://localhost:8053/resolve?domain=google.com&type=A"
curl "http://localhost:8053/resolve?domain=github.com&type=NS"
curl "http://localhost:8053/resolve?domain=nonexistent.invalid&type=A"
```

**Response:**
```json
{
  "Domain": "google.com",
  "Type": "A",
  "Records": [
    { "Type": "A", "Value": "142.250.80.46", "TTL": 300 }
  ],
  "Steps": [
    { "Stage": "root", "Server": "198.41.0.4", "Response": "REFERRAL → a.gtld-servers.net ...", "Duration": "12ms" },
    { "Stage": "tld",  "Server": "192.5.6.30",  "Response": "REFERRAL → ns1.google.com ...",      "Duration": "8ms"  },
    { "Stage": "authoritative", "Server": "216.239.32.10", "Response": "ANSWER: 1 record(s) ...", "Duration": "5ms"  }
  ],
  "Cached": false,
  "Latency": "145ms"
}
```

---

### `GET /stream?domain=<domain>&type=<type>`

Real-time streaming (NDJSON). Each line is a JSON event.

```bash
curl -N "http://localhost:8053/stream?domain=google.com&type=A"
```

Events emitted:

| event   | description                        |
|---------|------------------------------------|
| `start` | Resolution begins                  |
| `step`  | One hop in the resolution chain    |
| `done`  | Final records and summary          |

---

### `GET /cache`

Inspect all live cache entries with TTL remaining, hit counts, and stats.

```bash
curl "http://localhost:8053/cache"
```

---

### `POST /cache/flush`

Clear the entire cache.

```bash
curl -X POST "http://localhost:8053/cache/flush"
```

---

### `GET /health`

```bash
curl "http://localhost:8053/health"
```

---

## Testing Scenarios

### 1. Functional Resolution

```bash
# Standard A record
curl "http://localhost:8053/resolve?domain=google.com&type=A"

# CNAME chain (www often resolves via CNAME)
curl "http://localhost:8053/resolve?domain=www.github.com&type=A"

# NS records (verify traversal output)
curl "http://localhost:8053/resolve?domain=example.com&type=NS"

# MX record
curl "http://localhost:8053/resolve?domain=gmail.com&type=MX"
```

### 2. Caching Behavior

```bash
# First request — uncached (latency ~100-300ms)
curl "http://localhost:8053/resolve?domain=cloudflare.com&type=A"

# Second request — cache HIT (latency ~0ms)
curl "http://localhost:8053/resolve?domain=cloudflare.com&type=A"

# Inspect cache
curl "http://localhost:8053/cache"
```

### 3. Error Handling

```bash
# NXDOMAIN
curl "http://localhost:8053/resolve?domain=thisdoesnotexist.invalid&type=A"

# Returns: { "Error": "NXDOMAIN: thisdoesnotexist.invalid. does not exist" }
```

### 4. Real-Time Streaming

```bash
curl -N "http://localhost:8053/stream?domain=amazon.com&type=A"
```

### 5. Wireshark Capture

While running the server, open Wireshark on your active network interface with filter:

```
udp.port == 53
```

Then trigger a fresh (non-cached) resolution:

```bash
curl -X POST "http://localhost:8053/cache/flush"
curl "http://localhost:8053/resolve?domain=stackoverflow.com&type=A"
```

You will see:
- Queries to root server IPs (198.41.0.4, etc.)
- Referral responses with NS records
- Queries to TLD servers (e.g. a.gtld-servers.net)
- Final query to authoritative server
- All on UDP port 53

---

## Performance Metrics

Run the CLI tool twice on the same domain to compare latency:

```bash
go run ./cmd/dig stackoverflow.com A
# Observe: total latency ~150-400ms (cold)
# Second run: latency < 1ms (cache hit)
```

---

## Implementation Details

| Component | Details |
|-----------|---------|
| Protocol | UDP, port 53 |
| Packet format | Custom wire-format encoder/decoder (no stdlib `net` DNS) |
| Root servers | 5 hardcoded root server IPs (RFC 7720) |
| Recursion depth | Max 20 hops, max 10 CNAME redirects |
| Cache | TTL-based with background cleanup, thread-safe |
| Timeout | 5 seconds per UDP query, tries all servers |
| Supported types | A, AAAA, NS, CNAME, MX, TXT |
| CNAME handling | Automatic chain following |
| Glue records | Used when available to avoid extra lookups |

---

## Flags

| Flag | Default | Description |
|------|---------|-------------|
| `-http` | `:8053` | HTTP listen address |
| `-log` | `info` | Log level: `debug`, `info`, `error` |
