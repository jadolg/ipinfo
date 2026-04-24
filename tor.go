package main

import (
	"encoding/json"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"
)

var torHTTPClient = &http.Client{Timeout: 30 * time.Second}

const torOnionooURL = "https://onionoo.torproject.org/details?type=relay&running=true&flag=Exit&fields=exit_addresses,or_addresses"

type torExitSet struct {
	ips atomic.Pointer[map[string]struct{}]
	url string
}

func newTorExitSet() *torExitSet {
	t := &torExitSet{url: torOnionooURL}
	empty := make(map[string]struct{})
	t.ips.Store(&empty)
	return t
}

func (t *torExitSet) contains(ip string) bool {
	key := normalizeIP(ip)
	if key == "" {
		return false
	}
	m := t.ips.Load()
	_, ok := (*m)[key]
	return ok
}

func normalizeIP(s string) string {
	p := net.ParseIP(s)
	if p == nil {
		return ""
	}
	return p.String()
}

type onionooRelay struct {
	OrAddresses   []string `json:"or_addresses"`
	ExitAddresses []string `json:"exit_addresses"`
}

type onionooResponse struct {
	Relays []onionooRelay `json:"relays"`
}

func (t *torExitSet) refresh() {
	resp, err := torHTTPClient.Get(t.url)
	if err != nil {
		log.WithError(err).Error("tor list fetch failed")
		recordError("tor", "fetch")
		return
	}
	defer func(Body io.ReadCloser) {
		if err := Body.Close(); err != nil {
			log.WithError(err).Warn("could not close tor response body")
		}
	}(resp.Body)

	newIPs, err := parseOnionoo(resp.Body)
	if err != nil {
		log.WithError(err).Error("tor list decode error")
		recordError("tor", "decode")
		return
	}

	t.ips.Store(&newIPs)
	log.WithField("count", len(newIPs)).Info("tor exit list updated")
}

func parseOnionoo(r io.Reader) (map[string]struct{}, error) {
	var data onionooResponse
	if err := json.NewDecoder(r).Decode(&data); err != nil {
		return nil, err
	}
	ips := make(map[string]struct{}, len(data.Relays)*2)
	for _, relay := range data.Relays {
		for _, a := range relay.ExitAddresses {
			if key := normalizeIP(a); key != "" {
				ips[key] = struct{}{}
			}
		}
		for _, a := range relay.OrAddresses {
			host, _, err := net.SplitHostPort(a)
			if err != nil {
				continue
			}
			if key := normalizeIP(host); key != "" {
				ips[key] = struct{}{}
			}
		}
	}
	return ips, nil
}
