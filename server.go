package main

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	log "github.com/sirupsen/logrus"
)

type config struct {
	Addr        string
	CityDBPath  string
	ASNDBPath   string
	AccountID   string
	LicenseKey  string
	IPv4URL     string
	IPv6URL     string
	DBRefresh   time.Duration
	TorRefresh  time.Duration
	RedisAddr   string
	CacheTTL    time.Duration
	MetricsAddr string
	LogLevel    string
}

const (
	headerContentType = "Content-Type"
	rootPath          = "/"
)

type server struct {
	geo   *geoDB
	tor   *torExitSet
	cache *cache
}

func (s *server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set(headerContentType, "application/json")
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *server) handleJSON(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	parsed := net.ParseIP(ip)

	if parsed != nil {
		version := "4"
		if parsed.To4() == nil {
			version = "6"
		}
		ipVersionHits.WithLabelValues(version).Inc()
	}

	data := s.lookupIP(ip, parsed)
	w.Header().Set(headerContentType, "application/json")
	w.Header().Set("Access-Control-Allow-Origin", "*")
	_, _ = w.Write(data)
}

func (s *server) lookupIP(ip string, parsed net.IP) []byte {
	if s.cache != nil {
		cacheLookups.Inc()
		if data, ok := s.cache.get(ip); ok {
			cacheHits.Inc()
			return data
		}
	}

	info := IPInfo{IPAddress: ip}
	if parsed != nil {
		s.enrichFromDBs(&info, parsed)
	}
	if s.tor != nil {
		info.TorExit = s.tor.contains(ip)
	}

	data, _ := json.Marshal(info)

	if s.cache != nil {
		s.cache.set(ip, data)
	}
	return data
}

func (s *server) enrichFromDBs(info *IPInfo, parsed net.IP) {
	if s.geo == nil {
		return
	}
	if cityDB := s.geo.cityReader(); cityDB != nil {
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
	if asnDB := s.geo.asnReader(); asnDB != nil {
		if rec, err := asnDB.ASN(parsed); err == nil {
			info.ISP = rec.AutonomousSystemOrganization
		}
	}
}

func buildLocation(city, subdivision, country string) string {
	var b strings.Builder
	for _, s := range [3]string{city, subdivision, country} {
		if s == "" {
			continue
		}
		if b.Len() > 0 {
			b.WriteString(", ")
		}
		b.WriteString(s)
	}
	return b.String()
}

func clientIP(r *http.Request) string {
	if xri := r.Header.Get("X-Real-IP"); xri != "" {
		if ip := net.ParseIP(strings.TrimSpace(xri)); ip != nil {
			return ip.String()
		}
	}
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// For a single-proxy deployment the first entry is the client.
		first, _, _ := strings.Cut(xff, ",")
		if ip := net.ParseIP(strings.TrimSpace(first)); ip != nil {
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
	srv := &server{
		geo: newGeoDB(cfg),
	}
	srv.initTor(cfg.TorRefresh)
	if cfg.RedisAddr != "" {
		srv.cache = newCache(cfg.RedisAddr, cfg.CacheTTL)
		defer srv.cache.Close()
	}

	indexPage := renderIndex(indexConfig{
		IPv4URL: normalizeJSONURL(cfg.IPv4URL),
		IPv6URL: normalizeJSONURL(cfg.IPv6URL),
	})
	mux := http.NewServeMux()
	mux.HandleFunc("/json", withMetrics("/json", srv.handleJSON))
	mux.HandleFunc("/health", withMetrics("/health", srv.handleHealth))
	mux.HandleFunc("/{$}", withMetrics(rootPath, serveIndex(indexPage)))

	_, port, err := net.SplitHostPort(cfg.Addr)
	if err != nil {
		return fmt.Errorf("invalid addr %q: %w", cfg.Addr, err)
	}
	l4, l6, err := listenDualStack(port)
	if err != nil {
		return err
	}
	if cfg.MetricsAddr != "" {
		startMetricsServer(cfg.MetricsAddr)
	}
	log.WithField("port", port).Info("listening")

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
