package main

import (
	"bufio"
	"io"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
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
		log.Printf("tor list refresh failed: %v", err)
		return
	}
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Printf("tor list refresh failed: %v", err)
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
		log.Printf("tor list scan error: %v", err)
		return
	}

	t.ips.Store(&newIPs)
	log.Printf("tor exit list updated: %d nodes", len(newIPs))
}
