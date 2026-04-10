package main

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"time"

	"github.com/redis/go-redis/v9"
)

const cacheTimeout = 200 * time.Millisecond

type cache struct {
	rdb *redis.Client
	ttl time.Duration
}

func newCache(addr string, ttl time.Duration) *cache {
	rdb := redis.NewClient(&redis.Options{
		Addr:         addr,
		DialTimeout:  cacheTimeout,
		ReadTimeout:  cacheTimeout,
		WriteTimeout: cacheTimeout,
	})

	ctx, cancel := context.WithTimeout(context.Background(), cacheTimeout)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		log.Printf("warning: redis unavailable at %s: %v — will retry on use", addr, err)
	} else {
		log.Printf("redis cache connected: %s", addr)
	}
	return &cache{rdb: rdb, ttl: ttl}
}

func (c *cache) Close() {
	if err := c.rdb.Close(); err != nil {
		log.Printf("warning: could not close redis client: %v", err)
	}
}

func (c *cache) get(ip string) (IPInfo, bool) {
	ctx, cancel := context.WithTimeout(context.Background(), cacheTimeout)
	defer cancel()

	val, err := c.rdb.Get(ctx, ip).Bytes()
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
	ctx, cancel := context.WithTimeout(context.Background(), cacheTimeout)
	defer cancel()

	data, err := json.Marshal(info)
	if err != nil {
		log.Printf("cache encode %s: %v", ip, err)
		return
	}
	if err := c.rdb.Set(ctx, ip, data, c.ttl).Err(); err != nil {
		log.Printf("cache set %s: %v", ip, err)
	}
}
