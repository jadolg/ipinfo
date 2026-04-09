package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/oschwald/geoip2-golang"
)

var jsonBufPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

func (s *server) handleJSON(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	parsed := net.ParseIP(ip)

	info := IPInfo{IPAddress: ip}

	if parsed != nil && r.URL.Query().Has("hostname") {
		names, err := net.LookupAddr(ip)
		if err == nil && len(names) > 0 {
			info.Hostname = strings.TrimSuffix(names[0], ".")
		}
	}

	if cityDB := s.getCityDB(); cityDB != nil && parsed != nil {
		if rec, err := cityDB.City(parsed); err == nil {
			info.City = rec.City.Names["en"]
			info.Country = rec.Country.Names["en"]
			info.CountryCode = rec.Country.IsoCode

			var parts []string
			if info.City != "" {
				parts = append(parts, info.City)
			}
			if len(rec.Subdivisions) > 0 {
				if sub := rec.Subdivisions[0].IsoCode; sub != "" {
					parts = append(parts, sub)
				}
			}
			if info.Country != "" {
				parts = append(parts, info.Country)
			}
			info.Location = strings.Join(parts, ", ")
		}
	}

	if asnDB := s.getAsnDB(); asnDB != nil && parsed != nil {
		if rec, err := asnDB.ASN(parsed); err == nil {
			info.ISP = rec.AutonomousSystemOrganization
		}
	}

	if s.tor != nil {
		info.TorExit = s.tor.contains(ip)
	}

	buf := jsonBufPool.Get().(*bytes.Buffer)
	buf.Reset()
	_ = json.NewEncoder(buf).Encode(info)

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(buf.Bytes())

	jsonBufPool.Put(buf)
}

func clientIP(r *http.Request) string {
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if ip := net.ParseIP(strings.TrimSpace(xri)); ip != nil {
			return ip.String()
		}
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// For a single-proxy deployment the first entry is the client.
		parts := strings.Split(xff, ",")
		if ip := net.ParseIP(strings.TrimSpace(parts[0])); ip != nil {
			return ip.String()
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// normalizeJSONURL ensures raw has an https:// scheme and a /json path when
// neither is explicitly provided, so bare hostnames like "ipv4.example.com"
// work as well as full URLs like "https://ipv4.example.com/json".
func normalizeJSONURL(raw string) string {
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme == "" {
		u, err = url.Parse("https://" + raw)
		if err != nil {
			return raw
		}
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = "/json"
	}
	return u.String()
}

func run(_ context.Context, addr, cityDBPath, asnDBPath, accountID, licenseKey, ipv4URL, ipv6URL string, dbRefresh, torRefresh time.Duration) error {
	ipv4URL = normalizeJSONURL(ipv4URL)
	ipv6URL = normalizeJSONURL(ipv6URL)
	srv := &server{}

	if accountID != "" && licenseKey != "" {
		if dbsNeedRefresh(cityDBPath, asnDBPath, dbRefresh) {
			log.Printf("downloading GeoIP databases...")
			srv.refreshDBs(accountID, licenseKey, cityDBPath, asnDBPath)
		} else {
			log.Printf("GeoIP databases are fresh, skipping download")
			if cityDB, err := geoip2.Open(cityDBPath); err != nil {
				log.Printf("warning: could not open city DB: %v", err)
			} else {
				srv.cityDB.Store(cityDB)
			}
			if asnDB, err := geoip2.Open(asnDBPath); err != nil {
				log.Printf("warning: could not open ASN DB: %v", err)
			} else {
				srv.asnDB.Store(asnDB)
			}
		}
		go func() {
			ticker := time.NewTicker(dbRefresh)
			defer ticker.Stop()
			for range ticker.C {
				log.Printf("refreshing GeoIP databases...")
				srv.refreshDBs(accountID, licenseKey, cityDBPath, asnDBPath)
			}
		}()
	} else {
		if cityDB, err := geoip2.Open(cityDBPath); err != nil {
			log.Printf("warning: could not open city DB %q: %v", cityDBPath, err)
		} else {
			srv.cityDB.Store(cityDB)
			defer func(cityDB *geoip2.Reader) {
				err := cityDB.Close()
				if err != nil {
					log.Printf("warning: could not close city DB %q: %v", cityDBPath, err)
				}
			}(cityDB)
		}
		if asnDB, err := geoip2.Open(asnDBPath); err != nil {
			log.Printf("warning: could not open ASN DB %q: %v", asnDBPath, err)
		} else {
			srv.asnDB.Store(asnDB)
			defer func(asnDB *geoip2.Reader) {
				err := asnDB.Close()
				if err != nil {
					log.Printf("warning: could not close ASN DB %q: %v", asnDBPath, err)
				}
			}(asnDB)
		}
	}

	tor := newTorExitSet()
	srv.tor = tor
	tor.refresh()
	go func() {
		ticker := time.NewTicker(torRefresh)
		defer ticker.Stop()
		for range ticker.C {
			tor.refresh()
		}
	}()

	mux := http.NewServeMux()
	indexPage := renderIndex(indexConfig{IPv4URL: ipv4URL, IPv6URL: ipv6URL})

	mux.HandleFunc("/json", srv.handleJSON)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexPage)
	})

	_, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("invalid addr %q: %w", addr, err)
	}

	l4, err := net.Listen("tcp4", "0.0.0.0:"+port)
	if err != nil {
		return fmt.Errorf("listen tcp4: %w", err)
	}
	l6, err := net.Listen("tcp6", "[::]:"+port)
	if err != nil {
		return fmt.Errorf("listen tcp6: %w", err)
	}

	fmt.Printf("Listening on 0.0.0.0:%s and [::]:%s\n", port, port)

	httpSrv := &http.Server{
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  120 * time.Second,
	}

	errc := make(chan error, 2)
	for _, l := range []net.Listener{l4, l6} {
		go func(l net.Listener) {
			errc <- httpSrv.Serve(l)
		}(l)
	}
	return <-errc
}
