package resolver

import (
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"dns-resolver/cache"
	dns "dns-resolver/dns"
)

// bootstrapIP is the single unavoidable hardcoded value in any DNS resolver.
// It is the IP of a.root-servers.net — assigned by IANA in 1987 and essentially
// permanent. We use it exactly once: to discover the full set of root servers.
// After that, everything is self-discovered from live DNS responses.
const bootstrapIP = "198.41.0.4"

// tldsToDiscover is the list of TLD zones we pre-discover NS records for on
// startup. This prevents the gTLD bootstrapping loop (resolving k.gtld-servers.net
// requires a TLD server, which requires knowing k.gtld-servers.net's IP first).
// We discover these from the live root servers — no IPs are hardcoded here.
var tldsToDiscover = []string{
	"com.", "net.", "org.", "edu.", "gov.", "io.",
	"co.uk.", "uk.", "de.", "ca.", "au.",
}

// Step represents one hop in the DNS resolution process.
type Step struct {
	Stage    string
	Server   string
	Query    string
	Type     string
	Response string
	Cached   bool
	Duration time.Duration
	Error    string
}

// Result is the final output of a resolution attempt.
type Result struct {
	Domain      string
	Type        string
	Records     []cache.Record
	Steps       []Step
	Cached      bool
	Latency     time.Duration
	Error       string
	BootstrapMs int64 // how long the self-discovery phase took
}

// Resolver performs recursive DNS resolution.
type Resolver struct {
	cache       *cache.Cache
	logger      *log.Logger
	logLevel    string
	timeout     time.Duration
	rootServers []string // discovered at runtime, not hardcoded
	mu          sync.RWMutex
	ready       bool // true once bootstrap completed
}

// New creates a Resolver and runs the self-discovery bootstrap.
// The only hardcoded value used is bootstrapIP, and only during this phase.
func New(c *cache.Cache, logger *log.Logger, logLevel string) *Resolver {
	r := &Resolver{
		cache:    c,
		logger:   logger,
		logLevel: logLevel,
		timeout:  5 * time.Second,
	}
	r.bootstrap()
	return r
}

// RootServers returns the currently discovered root server IPs.
func (r *Resolver) RootServers() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, len(r.rootServers))
	copy(out, r.rootServers)
	return out
}

// ── Bootstrap ────────────────────────────────────────────────────────────────

// bootstrap performs the full self-discovery sequence:
//  1. Use bootstrapIP to fetch NS records for the root zone (".")
//     → learns all 13 root server hostnames + their IPs from glue records
//  2. Query the live root servers for NS records of common TLDs
//     → learns gTLD nameserver IPs from glue, breaking the bootstrapping loop
//
// After this function returns, rootServers is populated and the resolver
// never touches bootstrapIP again.
func (r *Resolver) bootstrap() {
	start := time.Now()
	r.logf("info", "Bootstrap: using seed IP %s to discover root servers...", bootstrapIP)

	// ── Step 1: discover root nameservers ────────────────────────────────────

	rootNSHosts, rootGlue, err := r.fetchNS(bootstrapIP, ".")
	if err != nil {
		r.logf("error", "Bootstrap: failed to fetch root NS: %v — falling back to seed IP only", err)
		r.mu.Lock()
		r.rootServers = []string{bootstrapIP}
		r.ready = true
		r.mu.Unlock()
		return
	}

	r.logf("info", "Bootstrap: discovered %d root NS records: %s",
		len(rootNSHosts), strings.Join(rootNSHosts, ", "))

	// Resolve each root NS hostname to an IP using the glue from the same response.
	// The root zone response always includes glue for all 13 root servers,
	// so we never need to make additional queries for this step.
	var rootIPs []string
	for _, host := range rootNSHosts {
		key := strings.ToLower(strings.TrimSuffix(host, ".")) + "."
		if ip, ok := rootGlue[key]; ok {
			rootIPs = append(rootIPs, ip)
			// Cache it permanently (very long TTL — root IPs are stable for years)
			r.cache.Set(key, "A", []cache.Record{{Type: "A", Value: ip, TTL: 518400}})
			r.logf("info", "Bootstrap: root server %s → %s (from glue)", host, ip)
		} else {
			r.logf("info", "Bootstrap: no glue for %s, skipping", host)
		}
	}

	if len(rootIPs) == 0 {
		r.logf("error", "Bootstrap: no root IPs resolved from glue, using seed")
		rootIPs = []string{bootstrapIP}
	}

	r.mu.Lock()
	r.rootServers = rootIPs
	r.mu.Unlock()

	r.logf("info", "Bootstrap: root servers ready (%d IPs)", len(rootIPs))

	// ── Step 2: discover gTLD nameservers ────────────────────────────────────
	// Query the live root servers for NS records of each common TLD.
	// The root server response includes glue A records for the TLD nameservers,
	// so we can populate the cache without any additional round-trips.

	r.logf("info", "Bootstrap: discovering gTLD nameservers from live root servers...")

	discovered := 0
	for _, tld := range tldsToDiscover {
		// Try each root server until one responds
		for _, rootIP := range rootIPs {
			nsHosts, glue, err := r.fetchNS(rootIP, tld)
			if err != nil || len(nsHosts) == 0 {
				continue
			}
			// Cache every gTLD nameserver IP found in glue
			for _, host := range nsHosts {
				key := strings.ToLower(strings.TrimSuffix(host, ".")) + "."
				if ip, ok := glue[key]; ok {
					r.cache.Set(key, "A", []cache.Record{{Type: "A", Value: ip, TTL: 172800}})
					discovered++
					r.logf("info", "Bootstrap: gTLD NS %s → %s (glue for %s)", host, ip, tld)
				}
			}
			break // got a response, move to next TLD
		}
	}

	r.mu.Lock()
	r.ready = true
	r.mu.Unlock()

	elapsed := time.Since(start)
	r.logf("info", "Bootstrap complete in %s — %d root IPs, %d gTLD NS IPs discovered",
		elapsed.Round(time.Millisecond), len(rootIPs), discovered)
}

// fetchNS sends a non-recursive NS query for zone to serverIP and returns
// the nameserver hostnames and a glue map of hostname→IP from the Additional section.
func (r *Resolver) fetchNS(serverIP, zone string) ([]string, map[string]string, error) {
	msg, err := r.query(serverIP, zone, dns.TypeNS)
	if err != nil {
		return nil, nil, err
	}

	var hosts []string
	glue := make(map[string]string)

	// NS records may be in Answers (if the server is authoritative) or Authority
	for _, rr := range append(msg.Answers, msg.Authority...) {
		if rr.Type == dns.TypeNS {
			host := strings.ToLower(strings.TrimSuffix(rr.RData, ".")) + "."
			hosts = append(hosts, host)
		}
	}
	for _, rr := range msg.Additional {
		if rr.Type == dns.TypeA {
			key := strings.ToLower(strings.TrimSuffix(rr.Name, ".")) + "."
			glue[key] = rr.RData
		}
	}
	return hosts, glue, nil
}

// ── Public resolve ────────────────────────────────────────────────────────────

// Resolve performs recursive DNS resolution for the given domain and record type.
func (r *Resolver) Resolve(domain, qtype string) *Result {
	start := time.Now()
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".") + "."
	qtypeCode := dns.TypeFromString(qtype)

	result := &Result{
		Domain: strings.TrimSuffix(domain, "."),
		Type:   strings.ToUpper(qtype),
	}

	// Check cache first
	if records, ok := r.cache.Get(domain, qtype); ok {
		result.Records = records
		result.Cached = true
		result.Latency = time.Since(start)
		result.Steps = append(result.Steps, Step{
			Stage:    "cache",
			Query:    domain,
			Type:     qtype,
			Response: fmt.Sprintf("Cache HIT — %d record(s)", len(records)),
			Cached:   true,
		})
		r.logf("info", "Cache HIT for %s %s", domain, qtype)
		return result
	}

	r.mu.RLock()
	roots := make([]string, len(r.rootServers))
	copy(roots, r.rootServers)
	r.mu.RUnlock()

	records, steps, err := r.recurse(domain, qtypeCode, roots, 0, "root")
	result.Steps = steps
	result.Latency = time.Since(start)

	if err != nil {
		result.Error = err.Error()
		return result
	}

	result.Records = records
	if len(records) > 0 {
		r.cache.Set(domain, qtype, records)
	}
	return result
}

// ── Recursive resolution ──────────────────────────────────────────────────────

const maxDepth = 20
const maxCNAMEChain = 10

func (r *Resolver) recurse(domain string, qtype uint16, servers []string, depth int, stage string) ([]cache.Record, []Step, error) {
	if depth > maxDepth {
		return nil, nil, errors.New("max recursion depth exceeded")
	}

	var allSteps []Step

	for _, server := range servers {
		r.logf("debug", "[%s] Querying %s for %s %s", stage, server, domain, dns.TypeName(qtype))

		start := time.Now()
		msg, err := r.query(server, domain, qtype)
		elapsed := time.Since(start)

		step := Step{
			Stage:    stage,
			Server:   server,
			Query:    domain,
			Type:     dns.TypeName(qtype),
			Duration: elapsed,
		}

		if err != nil {
			step.Error = err.Error()
			step.Response = fmt.Sprintf("ERROR: %s", err.Error())
			allSteps = append(allSteps, step)
			r.logf("error", "[%s] Query to %s failed: %v", stage, server, err)
			continue
		}

		rcode := msg.Rcode()
		if rcode == dns.RcodeNXDomain {
			step.Response = "NXDOMAIN — domain does not exist"
			allSteps = append(allSteps, step)
			return nil, allSteps, fmt.Errorf("NXDOMAIN: %s does not exist", domain)
		}
		if rcode == dns.RcodeServFail {
			step.Response = "SERVFAIL — server failure"
			allSteps = append(allSteps, step)
			continue
		}
		if rcode != dns.RcodeNoError {
			step.Response = fmt.Sprintf("Error rcode: %s", dns.RcodeString(rcode))
			allSteps = append(allSteps, step)
			continue
		}

		// Direct answer
		if len(msg.Answers) > 0 {
			var records []cache.Record
			var cnameTarget string

			for _, rr := range msg.Answers {
				if rr.Type == qtype {
					records = append(records, cache.Record{
						Type:  dns.TypeName(rr.Type),
						Value: rr.RData,
						TTL:   rr.TTL,
					})
				}
				if rr.Type == dns.TypeCNAME && qtype != dns.TypeCNAME {
					cnameTarget = strings.TrimSuffix(rr.RData, ".") + "."
					records = append(records, cache.Record{
						Type:  "CNAME",
						Value: strings.TrimSuffix(rr.RData, "."),
						TTL:   rr.TTL,
					})
				}
			}

			if len(records) > 0 && cnameTarget == "" {
				step.Response = fmt.Sprintf("ANSWER: %d record(s) — %s", len(records), summarizeRecords(records))
				allSteps = append(allSteps, step)
				return records, allSteps, nil
			}

			if cnameTarget != "" && depth < maxCNAMEChain {
				step.Response = fmt.Sprintf("CNAME → %s", strings.TrimSuffix(cnameTarget, "."))
				allSteps = append(allSteps, step)

				r.mu.RLock()
				roots := make([]string, len(r.rootServers))
				copy(roots, r.rootServers)
				r.mu.RUnlock()

				cnameRecords, cnameSteps, err := r.recurse(cnameTarget, qtype, roots, depth+1, "root")
				allSteps = append(allSteps, cnameSteps...)
				if err != nil {
					return records, allSteps, nil
				}
				return append(records, cnameRecords...), allSteps, nil
			}

			step.Response = fmt.Sprintf("ANSWER (partial): %d record(s)", len(records))
			allSteps = append(allSteps, step)
			return records, allSteps, nil
		}

		// Referral
		nsServers, glue := extractReferral(msg)
		if len(nsServers) == 0 {
			step.Response = "No answer and no referral — dead end"
			allSteps = append(allSteps, step)
			continue
		}

		step.Response = fmt.Sprintf("REFERRAL → %s (%d NS)", strings.Join(nsServers[:min(2, len(nsServers))], ", "), len(nsServers))
		allSteps = append(allSteps, step)

		nextServers, nsSteps, err := r.resolveNameservers(nsServers, glue, depth)
		allSteps = append(allSteps, nsSteps...)
		if err != nil || len(nextServers) == 0 {
			r.logf("error", "Could not resolve any NS for %s: %v", domain, err)
			continue
		}

		nextStage := "tld"
		if stage == "tld" || stage == "authoritative" {
			nextStage = "authoritative"
		}

		records, nextSteps, err := r.recurse(domain, qtype, nextServers, depth+1, nextStage)
		allSteps = append(allSteps, nextSteps...)
		if err != nil {
			return nil, allSteps, err
		}
		return records, allSteps, nil
	}

	return nil, allSteps, fmt.Errorf("resolution failed for %s: all servers exhausted", domain)
}

// resolveNameservers converts NS hostnames to IP addresses.
// Priority: glue records → cache → recursive lookup (from discovered root servers).
func (r *Resolver) resolveNameservers(nsHosts []string, glue map[string]string, depth int) ([]string, []Step, error) {
	var ips []string
	var allSteps []Step

	for _, ns := range nsHosts {
		ns = strings.ToLower(strings.TrimSuffix(ns, ".")) + "."

		// 1. Glue records from the referral response
		if ip, ok := glue[ns]; ok {
			r.logf("debug", "Glue: %s → %s", ns, ip)
			ips = append(ips, ip)
			r.cache.Set(ns, "A", []cache.Record{{Type: "A", Value: ip, TTL: 3600}})
			continue
		}

		// 2. Cache (includes bootstrap-discovered gTLD NS IPs)
		if cached, ok := r.cache.Get(ns, "A"); ok {
			for _, rec := range cached {
				ips = append(ips, rec.Value)
			}
			continue
		}

		// 3. Recursive A lookup using the self-discovered root servers
		r.logf("debug", "Resolving NS hostname recursively: %s", ns)
		r.mu.RLock()
		roots := make([]string, len(r.rootServers))
		copy(roots, r.rootServers)
		r.mu.RUnlock()

		nsIPs, nsSteps, err := r.recurse(ns, dns.TypeA, roots, depth+1, "root")
		allSteps = append(allSteps, nsSteps...)
		if err == nil && len(nsIPs) > 0 {
			for _, rec := range nsIPs {
				ips = append(ips, rec.Value)
			}
			r.cache.Set(ns, "A", nsIPs)
		}

		if len(ips) >= 4 {
			break
		}
	}

	if len(ips) == 0 {
		return nil, allSteps, errors.New("no IPs resolved for any nameserver")
	}
	return ips, allSteps, nil
}

// ── UDP query ─────────────────────────────────────────────────────────────────

func (r *Resolver) query(serverIP, domain string, qtype uint16) (*dns.Message, error) {
	id, err := randID()
	if err != nil {
		return nil, err
	}

	pkt, err := dns.BuildQuery(id, domain, qtype)
	if err != nil {
		return nil, err
	}

	addr := serverIP + ":53"
	conn, err := net.DialTimeout("udp", addr, r.timeout)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", addr, err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(r.timeout))

	if _, err = conn.Write(pkt); err != nil {
		return nil, fmt.Errorf("write to %s: %w", addr, err)
	}

	buf := make([]byte, 4096)
	n, err := conn.Read(buf)
	if err != nil {
		return nil, fmt.Errorf("read from %s: %w", addr, err)
	}

	msg, err := dns.Parse(buf[:n])
	if err != nil {
		return nil, fmt.Errorf("parse response from %s: %w", addr, err)
	}

	if msg.Header.ID != id {
		return nil, fmt.Errorf("ID mismatch: sent %d got %d", id, msg.Header.ID)
	}

	return msg, nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func extractReferral(msg *dns.Message) ([]string, map[string]string) {
	var ns []string
	glue := make(map[string]string)
	for _, rr := range msg.Authority {
		if rr.Type == dns.TypeNS {
			name := strings.ToLower(strings.TrimSuffix(rr.RData, ".")) + "."
			ns = append(ns, name)
		}
	}
	for _, rr := range msg.Additional {
		if rr.Type == dns.TypeA {
			key := strings.ToLower(strings.TrimSuffix(rr.Name, ".")) + "."
			glue[key] = rr.RData
		}
	}
	return ns, glue
}

func summarizeRecords(records []cache.Record) string {
	var vals []string
	for _, r := range records {
		vals = append(vals, r.Value)
	}
	if len(vals) > 3 {
		return strings.Join(vals[:3], ", ") + fmt.Sprintf(" (+%d more)", len(vals)-3)
	}
	return strings.Join(vals, ", ")
}

func randID() (uint16, error) {
	var b [2]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b[:]), nil
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func (r *Resolver) logf(level, format string, args ...interface{}) {
	switch r.logLevel {
	case "debug":
		r.logger.Printf("["+strings.ToUpper(level)+"] "+format, args...)
	case "info":
		if level != "debug" {
			r.logger.Printf("["+strings.ToUpper(level)+"] "+format, args...)
		}
	case "error":
		if level == "error" {
			r.logger.Printf("["+strings.ToUpper(level)+"] "+format, args...)
		}
	}
}
