package main

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	log "github.com/sirupsen/logrus"

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
		log.WithError(err).WithField("addr", addr).Warn("redis unavailable, will retry on use")
	} else {
		log.WithField("addr", addr).Info("redis cache connected")
	}
	return &cache{rdb: rdb, ttl: ttl}
}

func (c *cache) Close() {
	if err := c.rdb.Close(); err != nil {
		log.WithError(err).Warn("could not close redis client")
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
		log.WithError(err).WithField("ip", ip).Error("cache get failed")
		recordError("cache", "get")
		return IPInfo{}, false
	}
	var info IPInfo
	if err := json.Unmarshal(val, &info); err != nil {
		log.WithError(err).WithField("ip", ip).Error("cache decode failed")
		recordError("cache", "decode")
		return IPInfo{}, false
	}
	return info, true
}

func (c *cache) set(ip string, info IPInfo) {
	ctx, cancel := context.WithTimeout(context.Background(), cacheTimeout)
	defer cancel()

	data, err := json.Marshal(info)
	if err != nil {
		log.WithError(err).WithField("ip", ip).Error("cache encode failed")
		recordError("cache", "encode")
		return
	}
	if err := c.rdb.Set(ctx, ip, data, c.ttl).Err(); err != nil {
		log.WithError(err).WithField("ip", ip).Error("cache set failed")
		recordError("cache", "set")
	}
}
