package main

import (
	"context"
	"os"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli/v3"
)

func main() {
	var cfg config

	cmd := &cli.Command{
		Name:  "ipinfo",
		Usage: "IP information service",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "addr",
				Value:       ":8080",
				Usage:       "listen address (supports dual-stack IPv4+IPv6)",
				Sources:     cli.EnvVars("IPINFO_ADDR"),
				Destination: &cfg.Addr,
			},
			&cli.StringFlag{
				Name:        "city-db",
				Value:       "GeoLite2-City.mmdb",
				Usage:       "path to GeoLite2-City.mmdb",
				Sources:     cli.EnvVars("IPINFO_CITY_DB"),
				Destination: &cfg.CityDBPath,
			},
			&cli.StringFlag{
				Name:        "asn-db",
				Value:       "GeoLite2-ASN.mmdb",
				Usage:       "path to GeoLite2-ASN.mmdb",
				Sources:     cli.EnvVars("IPINFO_ASN_DB"),
				Destination: &cfg.ASNDBPath,
			},
			&cli.StringFlag{
				Name:        "account-id",
				Usage:       "MaxMind account ID for automatic DB downloads",
				Sources:     cli.EnvVars("IPINFO_ACCOUNT_ID"),
				Destination: &cfg.AccountID,
			},
			&cli.StringFlag{
				Name:        "license-key",
				Usage:       "MaxMind license key for automatic DB downloads",
				Sources:     cli.EnvVars("IPINFO_LICENSE_KEY"),
				Destination: &cfg.LicenseKey,
			},
			&cli.StringFlag{
				Name:        "ipv4-url",
				Usage:       "URL of the /json endpoint reachable over IPv4 (e.g. http://ipv4.example.com/json)",
				Sources:     cli.EnvVars("IPINFO_IPV4_URL"),
				Destination: &cfg.IPv4URL,
			},
			&cli.StringFlag{
				Name:        "ipv6-url",
				Usage:       "URL of the /json endpoint reachable over IPv6 (e.g. http://ipv6.example.com/json)",
				Sources:     cli.EnvVars("IPINFO_IPV6_URL"),
				Destination: &cfg.IPv6URL,
			},
			&cli.DurationFlag{
				Name:        "db-refresh",
				Value:       168 * time.Hour,
				Usage:       "how often to re-download GeoIP databases",
				Sources:     cli.EnvVars("IPINFO_DB_REFRESH"),
				Destination: &cfg.DBRefresh,
			},
			&cli.DurationFlag{
				Name:        "tor-refresh",
				Value:       1 * time.Hour,
				Usage:       "how often to refresh the Tor exit node list",
				Sources:     cli.EnvVars("IPINFO_TOR_REFRESH"),
				Destination: &cfg.TorRefresh,
			},
			&cli.StringFlag{
				Name:        "redis-addr",
				Usage:       "Redis address for IP info caching (e.g. redis:6379)",
				Sources:     cli.EnvVars("IPINFO_REDIS_ADDR"),
				Destination: &cfg.RedisAddr,
			},
			&cli.DurationFlag{
				Name:        "cache-ttl",
				Value:       6 * time.Hour,
				Usage:       "how long to cache IP info results in Redis",
				Sources:     cli.EnvVars("IPINFO_CACHE_TTL"),
				Destination: &cfg.CacheTTL,
			},
			&cli.StringFlag{
				Name:        "metrics-addr",
				Value:       ":9091",
				Usage:       "address to expose Prometheus metrics on (empty to disable)",
				Sources:     cli.EnvVars("IPINFO_METRICS_ADDR"),
				Destination: &cfg.MetricsAddr,
			},
			&cli.StringFlag{
				Name:        "log-level",
				Value:       "info",
				Usage:       "log level (trace, debug, info, warn, error)",
				Sources:     cli.EnvVars("IPINFO_LOG_LEVEL"),
				Destination: &cfg.LogLevel,
			},
		},
		Action: func(_ context.Context, _ *cli.Command) error {
			level, err := log.ParseLevel(cfg.LogLevel)
			if err != nil {
				log.WithField("level", cfg.LogLevel).Warn("unknown log level, defaulting to info")
				level = log.InfoLevel
			}
			log.SetLevel(level)
			log.SetFormatter(&log.TextFormatter{
				DisableColors: true,
				FullTimestamp: true,
			})
			return run(cfg)
		},
	}
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.WithError(err).Fatal("startup failed")
	}
}
