package main

import (
	"fmt"
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
