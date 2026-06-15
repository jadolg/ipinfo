package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	log "github.com/sirupsen/logrus"

	"github.com/oschwald/geoip2-golang"
)

// geoDBCloseGrace gives in-flight lookups time to finish before the old
// mmap'd reader is unmapped. Lookups take microseconds; 1 minute is ample.
const geoDBCloseGrace = 1 * time.Minute

// geoDownloadTimeout bounds each MaxMind download. The City DB is ~70 MB,
// so 5 minutes covers slow links while preventing a hung TLS connection
// from wedging the refresher forever.
const geoDownloadTimeout = 5 * time.Minute

var geoHTTPClient = &http.Client{Timeout: geoDownloadTimeout}

type geoDB struct {
	cityDB atomic.Pointer[geoip2.Reader]
	asnDB  atomic.Pointer[geoip2.Reader]
}

func newGeoDB(cfg config) *geoDB {
	g := &geoDB{}
	if cfg.AccountID != "" && cfg.LicenseKey != "" {
		if dbsNeedRefresh(cfg.CityDBPath, cfg.ASNDBPath, cfg.DBRefresh) {
			log.Info("downloading GeoIP databases")
			g.refresh(cfg.AccountID, cfg.LicenseKey, cfg.CityDBPath, cfg.ASNDBPath)
		} else {
			log.Info("GeoIP databases are fresh, skipping download")
			g.open(cfg.CityDBPath, cfg.ASNDBPath)
		}
		go func() {
			ticker := time.NewTicker(cfg.DBRefresh)
			defer ticker.Stop()
			for range ticker.C {
				log.Info("refreshing GeoIP databases")
				g.refresh(cfg.AccountID, cfg.LicenseKey, cfg.CityDBPath, cfg.ASNDBPath)
			}
		}()
	} else {
		g.open(cfg.CityDBPath, cfg.ASNDBPath)
	}
	// A DB whose content is older than two refresh cycles means refreshes are
	// not keeping it current; surface it loudly instead of serving stale geo data.
	g.warnIfStale(2 * cfg.DBRefresh)
	return g
}

func (g *geoDB) cityReader() *geoip2.Reader {
	return g.cityDB.Load()
}

func (g *geoDB) asnReader() *geoip2.Reader {
	return g.asnDB.Load()
}

// closeReaderAfterGrace unmaps the old reader after a grace period so that
// concurrent lookups holding the old pointer don't segfault on munmap'd memory.
func closeReaderAfterGrace(kind string, old *geoip2.Reader) {
	time.AfterFunc(geoDBCloseGrace, func() {
		if err := old.Close(); err != nil {
			log.WithError(err).Warnf("could not close old %s DB", kind)
		}
	})
}

func (g *geoDB) storeCity(db *geoip2.Reader) {
	if old := g.cityDB.Swap(db); old != nil {
		closeReaderAfterGrace("city", old)
	}
}

func (g *geoDB) storeASN(db *geoip2.Reader) {
	if old := g.asnDB.Swap(db); old != nil {
		closeReaderAfterGrace("ASN", old)
	}
}

// warnIfStale logs an error and increments ipinfo_errors_total when a loaded
// database's build date is older than maxAge, i.e. refreshes are not keeping it
// current (failing downloads or a restored volume that was never updated). This
// turns a silently stale database into a visible signal.
func (g *geoDB) warnIfStale(maxAge time.Duration) {
	check := func(kind string, r *geoip2.Reader) {
		if r == nil {
			return
		}
		age := time.Since(time.Unix(int64(r.Metadata().BuildEpoch), 0))
		if age > maxAge {
			log.WithField("db", kind).WithField("age", age.Round(time.Hour)).
				Error("GeoIP database is stale; refreshes are not keeping it current")
			recordError("geodb", "stale_"+kind)
		}
	}
	check("city", g.cityReader())
	check("asn", g.asnReader())
}

func (g *geoDB) open(cityDBPath, asnDBPath string) {
	if db, err := geoip2.Open(cityDBPath); err != nil {
		log.WithError(err).WithField("path", cityDBPath).Warn("could not open city DB")
		recordError("geodb", "open_city")
	} else {
		g.storeCity(db)
	}
	if db, err := geoip2.Open(asnDBPath); err != nil {
		log.WithError(err).WithField("path", asnDBPath).Warn("could not open ASN DB")
		recordError("geodb", "open_asn")
	} else {
		g.storeASN(db)
	}
}

func (g *geoDB) refresh(accountID, licenseKey, cityPath, asnPath string) {
	cityDB, err := downloadDB("GeoLite2-City", accountID, licenseKey, cityPath)
	if err != nil {
		log.WithError(err).WithField("edition", "GeoLite2-City").Error("DB refresh failed")
		recordError("geodb", "refresh_city")
	}
	asnDB, err := downloadDB("GeoLite2-ASN", accountID, licenseKey, asnPath)
	if err != nil {
		log.WithError(err).WithField("edition", "GeoLite2-ASN").Error("DB refresh failed")
		recordError("geodb", "refresh_asn")
	}

	if cityDB != nil {
		g.storeCity(cityDB)
	}
	if asnDB != nil {
		g.storeASN(asnDB)
	}
	if cityDB != nil || asnDB != nil {
		log.Info("GeoIP databases refreshed")
	}
}

// dbsNeedRefresh reports whether either database should be re-downloaded. It
// keys off each database's internal build timestamp rather than the file's
// mtime. A restored or copied volume (docker cp, backup restore, host
// migration) carries a recent mtime while holding stale content, which used to
// make a months-old DB look "fresh" and silently suppress downloads. The build
// epoch is the real content age.
func dbsNeedRefresh(cityPath, asnPath string, interval time.Duration) bool {
	return dbNeedsRefresh(cityPath, interval) || dbNeedsRefresh(asnPath, interval)
}

func dbNeedsRefresh(path string, interval time.Duration) bool {
	db, err := geoip2.Open(path)
	if err != nil {
		// Missing or unreadable: refresh to (re)create it.
		return true
	}
	defer func() { _ = db.Close() }()
	buildTime := time.Unix(int64(db.Metadata().BuildEpoch), 0)
	return time.Since(buildTime) > interval
}

func downloadDB(editionID, accountID, licenseKey, destPath string) (*geoip2.Reader, error) {
	body, err := fetchDB(editionID, accountID, licenseKey)
	if err != nil {
		return nil, err
	}
	defer func(body io.ReadCloser) {
		if err := body.Close(); err != nil {
			log.WithError(err).WithField("edition", editionID).Warn("could not close DB response body")
		}
	}(body)

	if err := extractAndSaveDB(editionID, body, destPath); err != nil {
		return nil, err
	}
	return geoip2.Open(destPath)
}

func fetchDB(editionID, accountID, licenseKey string) (io.ReadCloser, error) {
	url := fmt.Sprintf(
		"https://download.maxmind.com/geoip/databases/%s/download?suffix=tar.gz",
		editionID,
	)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", editionID, err)
	}
	req.SetBasicAuth(accountID, licenseKey)

	resp, err := geoHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download %s: %w", editionID, err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = resp.Body.Close()
		return nil, fmt.Errorf("download %s: HTTP %d", editionID, resp.StatusCode)
	}
	return resp.Body, nil
}

func extractAndSaveDB(editionID string, r io.Reader, destPath string) error {
	gz, err := gzip.NewReader(r)
	if err != nil {
		return fmt.Errorf("gzip %s: %w", editionID, err)
	}
	defer func(gz *gzip.Reader) {
		if err := gz.Close(); err != nil {
			log.WithError(err).WithField("edition", editionID).Warn("could not close gzip reader")
		}
	}(gz)

	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return fmt.Errorf("no .mmdb found in %s archive", editionID)
		}
		if err != nil {
			return fmt.Errorf("tar %s: %w", editionID, err)
		}
		if hdr.Typeflag != tar.TypeReg || !strings.HasSuffix(hdr.Name, ".mmdb") {
			continue
		}
		return saveAtomic(destPath, tr)
	}
}

func saveAtomic(destPath string, r io.Reader) error {
	dir := filepath.Dir(destPath)
	tmp, err := os.CreateTemp(dir, ".mmdb-download-*")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := io.Copy(tmp, r); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		_ = os.Remove(tmpName)
		return fmt.Errorf("rename %s: %w", destPath, err)
	}
	return nil
}
