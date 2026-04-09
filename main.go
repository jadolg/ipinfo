package main

import (
	"context"
	"log"
	"os"
	"time"

	"github.com/urfave/cli/v3"
)

func main() {
	var (
		addr       string
		cityDBPath string
		asnDBPath  string
		accountID  string
		licenseKey string
		ipv4URL    string
		ipv6URL    string
		dbRefresh  time.Duration
		torRefresh time.Duration
	)

	cmd := &cli.Command{
		Name:  "ipinfo",
		Usage: "IP information service",
		Flags: []cli.Flag{
			&cli.StringFlag{
				Name:        "addr",
				Value:       ":8080",
				Usage:       "listen address (supports dual-stack IPv4+IPv6)",
				Sources:     cli.EnvVars("IPINFO_ADDR"),
				Destination: &addr,
			},
			&cli.StringFlag{
				Name:        "city-db",
				Value:       "GeoLite2-City.mmdb",
				Usage:       "path to GeoLite2-City.mmdb",
				Sources:     cli.EnvVars("IPINFO_CITY_DB"),
				Destination: &cityDBPath,
			},
			&cli.StringFlag{
				Name:        "asn-db",
				Value:       "GeoLite2-ASN.mmdb",
				Usage:       "path to GeoLite2-ASN.mmdb",
				Sources:     cli.EnvVars("IPINFO_ASN_DB"),
				Destination: &asnDBPath,
			},
			&cli.StringFlag{
				Name:        "account-id",
				Usage:       "MaxMind account ID for automatic DB downloads",
				Sources:     cli.EnvVars("IPINFO_ACCOUNT_ID"),
				Destination: &accountID,
			},
			&cli.StringFlag{
				Name:        "license-key",
				Usage:       "MaxMind license key for automatic DB downloads",
				Sources:     cli.EnvVars("IPINFO_LICENSE_KEY"),
				Destination: &licenseKey,
			},
			&cli.StringFlag{
				Name:        "ipv4-url",
				Usage:       "URL of the /json endpoint reachable over IPv4 (e.g. http://ipv4.example.com/json)",
				Sources:     cli.EnvVars("IPINFO_IPV4_URL"),
				Destination: &ipv4URL,
			},
			&cli.StringFlag{
				Name:        "ipv6-url",
				Usage:       "URL of the /json endpoint reachable over IPv6 (e.g. http://ipv6.example.com/json)",
				Sources:     cli.EnvVars("IPINFO_IPV6_URL"),
				Destination: &ipv6URL,
			},
			&cli.DurationFlag{
				Name:        "db-refresh",
				Value:       168 * time.Hour,
				Usage:       "how often to re-download GeoIP databases",
				Sources:     cli.EnvVars("IPINFO_DB_REFRESH"),
				Destination: &dbRefresh,
			},
			&cli.DurationFlag{
				Name:        "tor-refresh",
				Value:       1 * time.Hour,
				Usage:       "how often to refresh the Tor exit node list",
				Sources:     cli.EnvVars("IPINFO_TOR_REFRESH"),
				Destination: &torRefresh,
			},
		},
		Action: func(ctx context.Context, _ *cli.Command) error {
			return run(ctx, addr, cityDBPath, asnDBPath, accountID, licenseKey, ipv4URL, ipv6URL, dbRefresh, torRefresh)
		},
	}
	if err := cmd.Run(context.Background(), os.Args); err != nil {
		log.Fatal(err)
	}
}
