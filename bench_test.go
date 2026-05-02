package main

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

// ---------- clientIP ----------

func BenchmarkClientIPFromRemoteAddr(b *testing.B) {
	req := httptest.NewRequest(http.MethodGet, "/json", nil)
	req.RemoteAddr = "203.0.113.42:51234"
	b.ResetTimer()
	for b.Loop() {
		clientIP(req)
	}
}

func BenchmarkClientIPFromXRealIP(b *testing.B) {
	req := httptest.NewRequest(http.MethodGet, "/json", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Real-IP", "198.51.100.7")
	b.ResetTimer()
	for b.Loop() {
		clientIP(req)
	}
}

func BenchmarkClientIPFromXForwardedFor(b *testing.B) {
	req := httptest.NewRequest(http.MethodGet, "/json", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "198.51.100.99, 10.0.0.1")
	b.ResetTimer()
	for b.Loop() {
		clientIP(req)
	}
}

// ---------- normalizeJSONURL ----------

func BenchmarkNormalizeJSONURL(b *testing.B) {
	cases := []string{
		"",
		"ipv4.example.com",
		"https://ipv4.example.com",
		"https://ipv4.example.com/json",
	}
	b.ResetTimer()
	for b.Loop() {
		for _, c := range cases {
			normalizeJSONURL(c)
		}
	}
}

// ---------- buildLocation ----------

func BenchmarkBuildLocation(b *testing.B) {
	b.ResetTimer()
	for b.Loop() {
		buildLocation("Mountain View", "CA", "United States")
	}
}

// ---------- torExitSet (atomic.Pointer — lock-free) ----------

func newBenchTorSet(n int) *torExitSet {
	t := newTorExitSet()
	m := make(map[string]struct{}, n)
	for i := range n {
		m[fmt.Sprintf("10.%d.%d.%d", i>>16&0xff, i>>8&0xff, i&0xff)] = struct{}{}
	}
	t.ips.Store(&m)
	return t
}

// BenchmarkTorContains measures a single-goroutine atomic.Pointer load + map lookup.
func BenchmarkTorContains(b *testing.B) {
	tor := newBenchTorSet(1000)
	b.ResetTimer()
	for b.Loop() {
		tor.contains("185.220.101.1")
	}
}

// BenchmarkTorContainsParallel measures parallel readers — the hot path in
// production. With -race it will catch any concurrent mutation bugs.
func BenchmarkTorContainsParallel(b *testing.B) {
	tor := newBenchTorSet(1000)
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tor.contains("185.220.101.1")
		}
	})
}

// BenchmarkTorConcurrentStore runs readers and a writer simultaneously.
// atomic.Pointer guarantees no data race; this verifies that readers always
// see a consistent (never partially-written) map and measures any overhead
// from ABA contention on the pointer. With -race this is a correctness test.
func BenchmarkTorConcurrentStore(b *testing.B) {
	tor := newBenchTorSet(500)

	var done atomic.Bool
	go func() {
		for !done.Load() {
			m := make(map[string]struct{}, 500)
			for i := range 500 {
				m[fmt.Sprintf("192.168.%d.%d", i>>8&0xff, i&0xff)] = struct{}{}
			}
			tor.ips.Store(&m)
		}
	}()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			tor.contains("185.220.101.1")
		}
	})

	done.Store(true)
}

// ---------- geoDB (atomic.Pointer — lock-free) ----------

// BenchmarkGeoDBAtomicLoad measures the cost of a pair of atomic.Pointer loads,
// which is what every request pays even when no GeoIP DB is configured.
func BenchmarkGeoDBAtomicLoad(b *testing.B) {
	g := &geoDB{}
	b.ResetTimer()
	for b.Loop() {
		_ = g.cityReader()
		_ = g.asnReader()
	}
}

// BenchmarkGeoDBConcurrentSwap runs parallel readers against a background
// goroutine that continuously swaps the pointer. Verifies that atomic swap +
// load is race-free and measures reader throughput during swaps.
func BenchmarkGeoDBConcurrentSwap(b *testing.B) {
	g := &geoDB{}

	var done atomic.Bool
	go func() {
		for !done.Load() {
			g.cityDB.Store(nil)
			g.asnDB.Store(nil)
		}
	}()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = g.cityReader()
			_ = g.asnReader()
		}
	})

	done.Store(true)
}

// ---------- handleJSON / sync.Pool ----------

// BenchmarkHandleJSON measures single-goroutine handler throughput including
// the sync.Pool buffer round-trip and Prometheus counter increments.
func BenchmarkHandleJSON(b *testing.B) {
	srv := newTestServer()
	b.ResetTimer()
	for b.Loop() {
		req := httptest.NewRequest(http.MethodGet, "/json", nil)
		req.RemoteAddr = "203.0.113.42:51234"
		rr := httptest.NewRecorder()
		srv.handleJSON(rr, req)
	}
}

// BenchmarkHandleJSONParallel exercises the sync.Pool under concurrent load.
// A pool misuse (e.g. returning a buffer still referenced by a writer) would
// show up as garbled output or a race under -race.
func BenchmarkHandleJSONParallel(b *testing.B) {
	srv := newTestServer()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/json", nil)
			req.RemoteAddr = "203.0.113.42:51234"
			rr := httptest.NewRecorder()
			srv.handleJSON(rr, req)
		}
	})
}

// BenchmarkHandleJSONIPv6Parallel measures the IPv6 path, which takes a
// different branch in the Prometheus ipVersionHits counter.
func BenchmarkHandleJSONIPv6Parallel(b *testing.B) {
	srv := newTestServer()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/json", nil)
			req.RemoteAddr = "[2001:db8::1]:51234"
			rr := httptest.NewRecorder()
			srv.handleJSON(rr, req)
		}
	})
}

// BenchmarkHandleJSONWithXForwardedForParallel mirrors a reverse-proxy
// deployment where every request carries an X-Forwarded-For header.
func BenchmarkHandleJSONWithXForwardedForParallel(b *testing.B) {
	srv := newTestServer()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/json", nil)
			req.RemoteAddr = "10.0.0.1:1234"
			req.Header.Set("X-Forwarded-For", "198.51.100.99, 10.0.0.1")
			rr := httptest.NewRecorder()
			srv.handleJSON(rr, req)
		}
	})
}

// ---------- Handler-level breakdown: MMDB lookup vs JSON marshal ----------
//
// These use httptest.NewRecorder (no TCP) to isolate individual costs.
// Compare against BenchmarkHandleJSON (no geo) to see the HTTP baseline.

var integrationIPs = []string{
	"8.8.8.8",
	"1.1.1.1",
	"208.67.222.222",
	"9.9.9.9",
	"185.220.101.1",
}

func parseIntegrationIP(i int) net.IP {
	return net.ParseIP(integrationIPs[i%len(integrationIPs)])
}

// BenchmarkHandleJSONWithMMDB measures the full handler cost with a real geoDB:
// MMDB lookup (city + ASN) + JSON marshal + HTTP write.
func BenchmarkHandleJSONWithMMDB(b *testing.B) {
	g := openTestGeoDB(b)
	srv := &server{geo: g, tor: newTorExitSet()}

	b.ResetTimer()
	var i int
	for b.Loop() {
		req := httptest.NewRequest(http.MethodGet, "/json", nil)
		req.Header.Set("X-Real-IP", integrationIPs[i%len(integrationIPs)])
		rr := httptest.NewRecorder()
		srv.handleJSON(rr, req)
		i++
	}
}

// BenchmarkEnrichFromDBs measures only the two MMDB tree lookups (city + ASN),
// with no HTTP overhead and no JSON marshal.
func BenchmarkEnrichFromDBs(b *testing.B) {
	g := openTestGeoDB(b)
	srv := &server{geo: g, tor: newTorExitSet()}

	b.ResetTimer()
	var i int
	for b.Loop() {
		info := IPInfo{IPAddress: integrationIPs[i%len(integrationIPs)]}
		srv.enrichFromDBs(&info, parseIntegrationIP(i))
		i++
	}
}

// BenchmarkJSONMarshalIPInfo measures only the json.Marshal cost on a populated
// IPInfo struct, with no MMDB lookup and no HTTP overhead.
func BenchmarkJSONMarshalIPInfo(b *testing.B) {
	info := IPInfo{
		IPAddress:   "8.8.8.8",
		City:        "Mountain View",
		Country:     "United States",
		CountryCode: "US",
		Location:    "Mountain View, CA, United States",
		ISP:         "Google LLC",
		TorExit:     false,
	}

	b.ResetTimer()
	for b.Loop() {
		_, _ = json.Marshal(info)
	}
}

// ---------- Integration benchmarks: full HTTP round-trip ----------
//
// These benchmarks spin up a real TCP listener (httptest.NewServer) and issue
// actual HTTP requests on localhost, giving end-to-end latency including TCP,
// HTTP parsing, GeoIP lookup, and JSON marshal.
//
// Run:
//
//	go test -run=^$ -bench=BenchmarkEndpoint -benchtime=5s -count=3 -benchmem

func startBenchServer(b *testing.B, srv *server) *httptest.Server {
	b.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/json", srv.handleJSON)
	ts := httptest.NewServer(mux)
	b.Cleanup(ts.Close)
	return ts
}

func httpGet(tb testing.TB, client *http.Client, baseURL, ip string) {
	tb.Helper()
	req, _ := http.NewRequest(http.MethodGet, baseURL+"/json", nil)
	req.Header.Set("X-Real-IP", ip)
	resp, err := client.Do(req)
	if err != nil {
		tb.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
}

// BenchmarkEndpointMMDB measures a full HTTP round-trip: MMDB lookup + JSON marshal over real TCP.
func BenchmarkEndpointMMDB(b *testing.B) {
	g := openTestGeoDB(b)
	srv := &server{geo: g, tor: newTorExitSet()}
	ts := startBenchServer(b, srv)
	client := ts.Client()

	b.ResetTimer()
	var i int
	for b.Loop() {
		httpGet(b, client, ts.URL, integrationIPs[i%len(integrationIPs)])
		i++
	}
}

func BenchmarkEndpointMMDBParallel(b *testing.B) {
	g := openTestGeoDB(b)
	srv := &server{geo: g, tor: newTorExitSet()}
	ts := startBenchServer(b, srv)
	client := ts.Client()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		var i int
		for pb.Next() {
			httpGet(b, client, ts.URL, integrationIPs[i%len(integrationIPs)])
			i++
		}
	})
}

// BenchmarkHandleJSONConcurrentTorRefresh runs handlers in parallel while a
// background goroutine continuously replaces the tor exit set. This is the
// closest simulation to production: HTTP traffic + background tor list refresh.
func BenchmarkHandleJSONConcurrentTorRefresh(b *testing.B) {
	srv := newTestServer("185.220.101.1")

	var done atomic.Bool
	go func() {
		for !done.Load() {
			m := make(map[string]struct{}, 10)
			m["185.220.101.1"] = struct{}{}
			srv.tor.ips.Store(&m)
		}
	}()

	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			req := httptest.NewRequest(http.MethodGet, "/json", nil)
			req.RemoteAddr = "185.220.101.1:9001"
			rr := httptest.NewRecorder()
			srv.handleJSON(rr, req)
		}
	})

	done.Store(true)
}
