package main

import (
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestServer(torIPs ...string) *server {
	tor := newTorExitSet()
	if len(torIPs) > 0 {
		m := make(map[string]struct{}, len(torIPs))
		for _, ip := range torIPs {
			m[ip] = struct{}{}
		}
		tor.ips.Store(&m)
	}
	return &server{tor: tor}
}

func getJSON(t *testing.T, srv *server, remoteAddr string, headers map[string]string) IPInfo {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/json", nil)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	srv.handleJSON(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("missing CORS header")
	}

	var info IPInfo
	if err := json.NewDecoder(rr.Body).Decode(&info); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return info
}

func TestIPv4FromRemoteAddr(t *testing.T) {
	srv := newTestServer()
	info := getJSON(t, srv, "203.0.113.42:51234", nil)

	if info.IPAddress != "203.0.113.42" {
		t.Errorf("IP = %q, want 203.0.113.42", info.IPAddress)
	}
}

func TestIPv6FromRemoteAddr(t *testing.T) {
	srv := newTestServer()
	info := getJSON(t, srv, "[2001:db8::1]:51234", nil)

	if info.IPAddress != "2001:db8::1" {
		t.Errorf("IP = %q, want 2001:db8::1", info.IPAddress)
	}
}

func TestXRealIPOverridesRemoteAddr(t *testing.T) {
	srv := newTestServer()
	info := getJSON(t, srv, "10.0.0.1:1234", map[string]string{
		"X-Real-IP": "198.51.100.7",
	})

	if info.IPAddress != "198.51.100.7" {
		t.Errorf("IP = %q, want 198.51.100.7", info.IPAddress)
	}
}

func TestXForwardedForOverridesRemoteAddr(t *testing.T) {
	srv := newTestServer()
	info := getJSON(t, srv, "10.0.0.1:1234", map[string]string{
		"X-Forwarded-For": "198.51.100.99, 10.0.0.1",
	})

	if info.IPAddress != "198.51.100.99" {
		t.Errorf("IP = %q, want 198.51.100.99", info.IPAddress)
	}
}

func TestXRealIPTakesPriorityOverXForwardedFor(t *testing.T) {
	srv := newTestServer()
	info := getJSON(t, srv, "10.0.0.1:1234", map[string]string{
		"X-Real-IP":       "198.51.100.1",
		"X-Forwarded-For": "198.51.100.2",
	})

	if info.IPAddress != "198.51.100.1" {
		t.Errorf("IP = %q, want 198.51.100.1", info.IPAddress)
	}
}

func TestTorExitTrue(t *testing.T) {
	torIP := "185.220.101.1"
	srv := newTestServer(torIP)
	info := getJSON(t, srv, torIP+":9001", nil)

	if !info.TorExit {
		t.Error("TorExit = false, want true for known Tor exit IP")
	}
}

func TestTorExitFalse(t *testing.T) {
	srv := newTestServer("185.220.101.1")
	info := getJSON(t, srv, "203.0.113.42:1234", nil)

	if info.TorExit {
		t.Error("TorExit = true, want false for non-Tor IP")
	}
}

func TestAllFieldsPresent(t *testing.T) {
	srv := newTestServer()
	req := httptest.NewRequest(http.MethodGet, "/json", nil)
	req.RemoteAddr = "203.0.113.1:1234"
	rr := httptest.NewRecorder()
	srv.handleJSON(rr, req)

	var raw map[string]any
	if err := json.NewDecoder(rr.Body).Decode(&raw); err != nil {
		t.Fatalf("decode: %v", err)
	}

	required := []string{
		"IPAddress",
		"Location",
		"ISP",
		"TorExit",
		"City",
		"Country",
		"CountryCode",
	}
	for _, field := range required {
		if _, ok := raw[field]; !ok {
			t.Errorf("missing field %q in JSON response", field)
		}
	}
}

func newMux(srv *server, cfg indexConfig) *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/json", srv.handleJSON)
	mux.HandleFunc("/health", srv.handleHealth)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_ = indexTmpl.Execute(w, cfg)
	})
	return mux
}

func TestRootServesHTML(t *testing.T) {
	rr := httptest.NewRecorder()
	newMux(newTestServer(), indexConfig{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("Content-Type = %q, want text/html; charset=utf-8", ct)
	}
	if !strings.Contains(rr.Body.String(), "makeCard") {
		t.Error("HTML body does not contain expected makeCard JS function")
	}
}

func TestRootShowsBothProtocols(t *testing.T) {
	cfg := indexConfig{IPv4URL: "http://127.0.0.1:9090/json", IPv6URL: "http://[::1]:9090/json"}
	rr := httptest.NewRecorder()
	newMux(newTestServer(), cfg).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	body := rr.Body.String()
	if !strings.Contains(body, "http://127.0.0.1:9090/json") {
		t.Error("HTML body missing IPv4 URL")
	}
	if !strings.Contains(body, "http://[::1]:9090/json") {
		t.Error("HTML body missing IPv6 URL")
	}
}

func TestServerEnrichesFromGeoDB(t *testing.T) {
	g := openTestGeoDB(t)

	srv := &server{geo: g, tor: newTorExitSet()}

	info := IPInfo{IPAddress: "8.8.8.8"}
	srv.enrichFromDBs(&info, net.ParseIP("8.8.8.8"))

	if info.Country == "" {
		t.Error("Country should not be empty for 8.8.8.8")
	}
	if info.CountryCode == "" {
		t.Error("CountryCode should not be empty for 8.8.8.8")
	}
	if info.ISP == "" {
		t.Error("ISP should not be empty for 8.8.8.8")
	}
}

func TestServerWithNilGeoSkipsEnrichment(t *testing.T) {
	srv := &server{tor: newTorExitSet()}

	info := IPInfo{IPAddress: "8.8.8.8"}
	srv.enrichFromDBs(&info, net.ParseIP("8.8.8.8"))

	if info.Country != "" || info.ISP != "" {
		t.Error("enrichFromDBs with nil geo should not populate geo fields")
	}
}

func TestUnknownPathReturns404(t *testing.T) {
	rr := httptest.NewRecorder()
	newMux(newTestServer(), indexConfig{}).ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/xml", nil))

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}
