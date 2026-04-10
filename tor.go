package main

import (
	"bufio"
	"io"
	"net/http"
	"strings"
	"sync/atomic"

	log "github.com/sirupsen/logrus"
)

type torExitSet struct {
	ips atomic.Pointer[map[string]struct{}]
}

func newTorExitSet() *torExitSet {
	t := &torExitSet{}
	empty := make(map[string]struct{})
	t.ips.Store(&empty)
	return t
}

func (t *torExitSet) contains(ip string) bool {
	m := t.ips.Load()
	_, ok := (*m)[ip]
	return ok
}

func (t *torExitSet) refresh() {
	resp, err := http.Get("https://check.torproject.org/torbulkexitlist")
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

	newIPs := make(map[string]struct{})
	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line != "" && !strings.HasPrefix(line, "#") {
			newIPs[line] = struct{}{}
		}
	}
	if err := scanner.Err(); err != nil {
		log.WithError(err).Error("tor list scan error")
		recordError("tor", "scan")
		return
	}

	t.ips.Store(&newIPs)
	log.WithField("count", len(newIPs)).Info("tor exit list updated")
}
