package main

import (
	"bytes"
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

type config struct {
	Addr       string
	CityDBPath string
	ASNDBPath  string
	AccountID  string
	LicenseKey string
	IPv4URL    string
	IPv6URL    string
	DBRefresh  time.Duration
	TorRefresh time.Duration
}

var jsonBufPool = sync.Pool{
	New: func() any { return new(bytes.Buffer) },
}

func (s *server) handleJSON(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	parsed := net.ParseIP(ip)

	info := IPInfo{IPAddress: ip}
	if parsed != nil {
		s.enrichFromDBs(&info, parsed)
	}
	if parsed != nil && r.URL.Query().Has("hostname") {
		info.Hostname = reverseLookup(ip)
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

func (s *server) enrichFromDBs(info *IPInfo, parsed net.IP) {
	if cityDB := s.getCityDB(); cityDB != nil {
		if rec, err := cityDB.City(parsed); err == nil {
			info.City = rec.City.Names["en"]
			info.Country = rec.Country.Names["en"]
			info.CountryCode = rec.Country.IsoCode
			var subdivision string
			if len(rec.Subdivisions) > 0 {
				subdivision = rec.Subdivisions[0].IsoCode
			}
			info.Location = buildLocation(info.City, subdivision, info.Country)
		}
	}
	if asnDB := s.getAsnDB(); asnDB != nil {
		if rec, err := asnDB.ASN(parsed); err == nil {
			info.ISP = rec.AutonomousSystemOrganization
		}
	}
}

func buildLocation(city, subdivision, country string) string {
	var parts []string
	for _, s := range []string{city, subdivision, country} {
		if s != "" {
			parts = append(parts, s)
		}
	}
	return strings.Join(parts, ", ")
}

func reverseLookup(ip string) string {
	names, err := net.LookupAddr(ip)
	if err != nil || len(names) == 0 {
		return ""
	}
	return strings.TrimSuffix(names[0], ".")
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

func (s *server) initDBs(cfg config) {
	if cfg.AccountID != "" && cfg.LicenseKey != "" {
		if dbsNeedRefresh(cfg.CityDBPath, cfg.ASNDBPath, cfg.DBRefresh) {
			log.Printf("downloading GeoIP databases...")
			s.refreshDBs(cfg.AccountID, cfg.LicenseKey, cfg.CityDBPath, cfg.ASNDBPath)
		} else {
			log.Printf("GeoIP databases are fresh, skipping download")
			s.openDBs(cfg.CityDBPath, cfg.ASNDBPath)
		}
		go func() {
			ticker := time.NewTicker(cfg.DBRefresh)
			defer ticker.Stop()
			for range ticker.C {
				log.Printf("refreshing GeoIP databases...")
				s.refreshDBs(cfg.AccountID, cfg.LicenseKey, cfg.CityDBPath, cfg.ASNDBPath)
			}
		}()
	} else {
		s.openDBs(cfg.CityDBPath, cfg.ASNDBPath)
	}
}

func (s *server) openDBs(cityDBPath, asnDBPath string) {
	if cityDB, err := geoip2.Open(cityDBPath); err != nil {
		log.Printf("warning: could not open city DB %q: %v", cityDBPath, err)
	} else {
		s.storeCityDB(cityDB)
	}
	if asnDB, err := geoip2.Open(asnDBPath); err != nil {
		log.Printf("warning: could not open ASN DB %q: %v", asnDBPath, err)
	} else {
		s.storeAsnDB(asnDB)
	}
}

func (s *server) initTor(torRefresh time.Duration) {
	tor := newTorExitSet()
	s.tor = tor
	tor.refresh()
	go func() {
		ticker := time.NewTicker(torRefresh)
		defer ticker.Stop()
		for range ticker.C {
			tor.refresh()
		}
	}()
}

func listenDualStack(port string) (net.Listener, net.Listener, error) {
	l4, err := net.Listen("tcp4", "0.0.0.0:"+port)
	if err != nil {
		return nil, nil, fmt.Errorf("listen tcp4: %w", err)
	}
	l6, err := net.Listen("tcp6", "[::]:"+port)
	if err != nil {
		_ = l4.Close()
		return nil, nil, fmt.Errorf("listen tcp6: %w", err)
	}
	return l4, l6, nil
}

func run(cfg config) error {
	srv := &server{}
	srv.initDBs(cfg)
	srv.initTor(cfg.TorRefresh)

	indexPage := renderIndex(indexConfig{
		IPv4URL: normalizeJSONURL(cfg.IPv4URL),
		IPv6URL: normalizeJSONURL(cfg.IPv6URL),
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/json", srv.handleJSON)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(indexPage)
	})

	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return fmt.Errorf("invalid addr %q: %w", cfg.Addr, err)
	}
	l4, l6, err := listenDualStack(port)
	if err != nil {
		return err
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
