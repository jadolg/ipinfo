# ipinfo

Shows your public IP address along with hostname, ISP, location, and whether
you are connecting through a Tor exit node. Handles IPv4 and IPv6 separately
so you can see both at once.

Live at: https://ip.akiel.dev

## Endpoints

- `GET /` -- HTML page with IP details
- `GET /json` -- same data as JSON

## Running with Docker Compose

```
docker compose up -d
```

The service listens on port 8080. Put a reverse proxy (e.g. Caddy) in front of it.

## Configuration

All options can be set as environment variables or CLI flags.

| Variable | Flag | Default | Description |
|---|---|---|---|
| `IPINFO_ADDR` | `--addr` | `:8080` | Listen address |
| `IPINFO_CITY_DB` | `--city-db` | `GeoLite2-City.mmdb` | Path to GeoLite2-City database |
| `IPINFO_ASN_DB` | `--asn-db` | `GeoLite2-ASN.mmdb` | Path to GeoLite2-ASN database |
| `IPINFO_ACCOUNT_ID` | `--account-id` | | MaxMind account ID (enables automatic DB downloads) |
| `IPINFO_LICENSE_KEY` | `--license-key` | | MaxMind license key |
| `IPINFO_DB_REFRESH` | `--db-refresh` | `168h` | How often to re-download GeoIP databases |
| `IPINFO_TOR_REFRESH` | `--tor-refresh` | `1h` | How often to refresh the Tor exit node list |
| `IPINFO_IPV4_URL` | `--ipv4-url` | | URL of the `/json` endpoint reachable over IPv4 (e.g. `https://ipv4.example.com/json`) |
| `IPINFO_IPV6_URL` | `--ipv6-url` | | URL of the `/json` endpoint reachable over IPv6 (e.g. `https://ipv6.example.com/json`) |

When both `IPINFO_ACCOUNT_ID` and `IPINFO_LICENSE_KEY` are set, the service
downloads and periodically refreshes the GeoLite2 databases automatically.
Otherwise it uses the database files at the configured paths.

When both `IPINFO_IPV4_URL` and `IPINFO_IPV6_URL` are set, the UI shows two
cards side by side -- one for each protocol. This requires separate DNS records
pointing `ipv4.*` to only an A record and `ipv6.*` to only an AAAA record,
with the main domain having both.
