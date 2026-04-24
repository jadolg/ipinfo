package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNormalizeIP(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"1.2.3.4", "1.2.3.4"},
		{"  1.2.3.4  ", ""},
		{"2001:db8::1", "2001:db8::1"},
		{"2001:0db8:0000:0000:0000:0000:0000:0001", "2001:db8::1"},
		{"2001:DB8::1", "2001:db8::1"},
		{"not-an-ip", ""},
		{"", ""},
	}
	for _, c := range cases {
		got := normalizeIP(c.in)
		if got != c.want {
			t.Errorf("normalizeIP(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestTorContainsIPv6Canonicalizes(t *testing.T) {
	set := newTorExitSet()
	m := map[string]struct{}{
		"2001:db8::1": {},
		"1.2.3.4":     {},
	}
	set.ips.Store(&m)

	cases := []struct {
		ip   string
		want bool
	}{
		{"2001:db8::1", true},
		{"2001:0db8:0000:0000:0000:0000:0000:0001", true},
		{"2001:DB8::1", true},
		{"2001:db8::2", false},
		{"1.2.3.4", true},
		{"1.2.3.5", false},
		{"garbage", false},
	}
	for _, c := range cases {
		if got := set.contains(c.ip); got != c.want {
			t.Errorf("contains(%q) = %v, want %v", c.ip, got, c.want)
		}
	}
}

const sampleOnionoo = `{
  "relays": [
    {
      "or_addresses": ["1.2.3.4:9001", "[2001:db8::1]:9001"],
      "exit_addresses": ["1.2.3.4", "2001:db8::1"]
    },
    {
      "or_addresses": ["[2001:0db8:0000:0000:0000:0000:0000:0002]:443"],
      "exit_addresses": []
    },
    {
      "or_addresses": ["bad-addr-no-port"],
      "exit_addresses": ["not-an-ip"]
    }
  ]
}`

func TestParseOnionooIncludesIPv6(t *testing.T) {
	got, err := parseOnionoo(strings.NewReader(sampleOnionoo))
	if err != nil {
		t.Fatalf("parseOnionoo error: %v", err)
	}
	want := []string{"1.2.3.4", "2001:db8::1", "2001:db8::2"}
	for _, ip := range want {
		if _, ok := got[ip]; !ok {
			t.Errorf("missing %q in parsed set, got %v", ip, got)
		}
	}
	if _, ok := got["not-an-ip"]; ok {
		t.Error("invalid IP leaked into parsed set")
	}
	if _, ok := got["bad-addr-no-port"]; ok {
		t.Error("invalid or_address leaked into parsed set")
	}
	if len(got) != len(want) {
		t.Errorf("got %d entries, want %d: %v", len(got), len(want), got)
	}
}

func TestParseOnionooMalformed(t *testing.T) {
	if _, err := parseOnionoo(strings.NewReader("{not json")); err == nil {
		t.Error("expected error for malformed JSON")
	}
}

func TestTorRefreshPopulatesSet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(sampleOnionoo))
	}))
	defer srv.Close()

	set := newTorExitSet()
	set.url = srv.URL
	set.refresh()

	if !set.contains("2001:db8::1") {
		t.Error("expected IPv6 exit to be detected after refresh")
	}
	if !set.contains("2001:0db8:0000:0000:0000:0000:0000:0001") {
		t.Error("expected non-canonical IPv6 form to match after refresh")
	}
	if !set.contains("1.2.3.4") {
		t.Error("expected IPv4 exit to be detected")
	}
	if set.contains("8.8.8.8") {
		t.Error("non-tor IP should not be in set")
	}
}

func TestTorRefreshDecodeFailureKeepsOldSet(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("{broken"))
	}))
	defer srv.Close()

	set := newTorExitSet()
	set.url = srv.URL
	existing := map[string]struct{}{"9.9.9.9": {}}
	set.ips.Store(&existing)

	set.refresh()

	if !set.contains("9.9.9.9") {
		t.Error("decode failure must not wipe existing set")
	}
}
