package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

const cacheTTL = 1 * time.Hour

type cache struct {
	rdb *redis.Client
}

func newCache(addr string) *cache {
	rdb := redis.NewClient(&redis.Options{Addr: addr})
	if err := rdb.Ping(context.Background()).Err(); err != nil {
		log.Printf("warning: redis unavailable at %s: %v — caching disabled", addr, err)
		return nil
	}
	log.Printf("redis cache connected: %s", addr)
	return &cache{rdb: rdb}
}

func (c *cache) get(ip string) (IPInfo, bool) {
	val, err := c.rdb.Get(context.Background(), ip).Bytes()
	if errors.Is(err, redis.Nil) {
		return IPInfo{}, false
	}
	if err != nil {
		log.Printf("cache get %s: %v", ip, err)
		return IPInfo{}, false
	}
	var info IPInfo
	if err := json.Unmarshal(val, &info); err != nil {
		log.Printf("cache decode %s: %v", ip, err)
		return IPInfo{}, false
	}
	return info, true
}

func (c *cache) set(ip string, info IPInfo) {
	data, err := json.Marshal(info)
	if err != nil {
		log.Printf("cache encode %s: %v", ip, err)
		return
	}
	if err := c.rdb.Set(context.Background(), ip, data, cacheTTL).Err(); err != nil {
		log.Printf("cache set %s: %v", ip, err)
	}
}
