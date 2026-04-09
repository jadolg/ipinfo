package main

import (
	"bufio"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
)

type torExitSet struct {
	mu  sync.RWMutex
	ips map[string]struct{}
}

func (t *torExitSet) contains(ip string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	_, ok := t.ips[ip]
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

	t.mu.Lock()
	t.ips = newIPs
	t.mu.Unlock()
	log.Printf("tor exit list updated: %d nodes", len(newIPs))
}
