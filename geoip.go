package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"time"

	"github.com/oschwald/geoip2-golang"
)

type geoDB struct {
	cityDB atomic.Pointer[geoip2.Reader]
	asnDB  atomic.Pointer[geoip2.Reader]
}

func newGeoDB(cfg config) *geoDB {
	g := &geoDB{}
	if cfg.AccountID != "" && cfg.LicenseKey != "" {
		if dbsNeedRefresh(cfg.CityDBPath, cfg.ASNDBPath, cfg.DBRefresh) {
			log.Printf("downloading GeoIP databases...")
			g.refresh(cfg.AccountID, cfg.LicenseKey, cfg.CityDBPath, cfg.ASNDBPath)
		} else {
			log.Printf("GeoIP databases are fresh, skipping download")
			g.open(cfg.CityDBPath, cfg.ASNDBPath)
		}
		go func() {
			ticker := time.NewTicker(cfg.DBRefresh)
			defer ticker.Stop()
			for range ticker.C {
				log.Printf("refreshing GeoIP databases...")
				g.refresh(cfg.AccountID, cfg.LicenseKey, cfg.CityDBPath, cfg.ASNDBPath)
			}
		}()
	} else {
		g.open(cfg.CityDBPath, cfg.ASNDBPath)
	}
	return g
}

func (g *geoDB) cityReader() *geoip2.Reader {
	return g.cityDB.Load()
}

func (g *geoDB) asnReader() *geoip2.Reader {
	return g.asnDB.Load()
}

func (g *geoDB) storeCity(db *geoip2.Reader) {
	if old := g.cityDB.Swap(db); old != nil {
		if err := old.Close(); err != nil {
			log.Printf("warning: could not close old city DB: %v", err)
		}
	}
}

func (g *geoDB) storeASN(db *geoip2.Reader) {
	if old := g.asnDB.Swap(db); old != nil {
		if err := old.Close(); err != nil {
			log.Printf("warning: could not close old ASN DB: %v", err)
		}
	}
}

func (g *geoDB) open(cityDBPath, asnDBPath string) {
	if db, err := geoip2.Open(cityDBPath); err != nil {
		log.Printf("warning: could not open city DB %q: %v", cityDBPath, err)
		recordError("geodb", "open_city")
	} else {
		g.storeCity(db)
	}
	if db, err := geoip2.Open(asnDBPath); err != nil {
		log.Printf("warning: could not open ASN DB %q: %v", asnDBPath, err)
		recordError("geodb", "open_asn")
	} else {
		g.storeASN(db)
	}
}

func (g *geoDB) refresh(accountID, licenseKey, cityPath, asnPath string) {
	cityDB, err := downloadDB("GeoLite2-City", accountID, licenseKey, cityPath)
	if err != nil {
		log.Printf("GeoLite2-City refresh failed: %v", err)
		recordError("geodb", "refresh_city")
	}
	asnDB, err := downloadDB("GeoLite2-ASN", accountID, licenseKey, asnPath)
	if err != nil {
		log.Printf("GeoLite2-ASN refresh failed: %v", err)
		recordError("geodb", "refresh_asn")
	}

	if cityDB != nil {
		g.storeCity(cityDB)
	}
	if asnDB != nil {
		g.storeASN(asnDB)
	}
	if cityDB != nil || asnDB != nil {
		log.Printf("GeoIP databases refreshed")
	}
}

func dbsNeedRefresh(cityPath, asnPath string, interval time.Duration) bool {
	for _, path := range []string{cityPath, asnPath} {
		fi, err := os.Stat(path)
		if err != nil || time.Since(fi.ModTime()) > interval {
			return true
		}
	}
	return false
}

func downloadDB(editionID, accountID, licenseKey, destPath string) (*geoip2.Reader, error) {
	body, err := fetchDB(editionID, accountID, licenseKey)
	if err != nil {
		return nil, err
	}
	defer func(body io.ReadCloser) {
		if err := body.Close(); err != nil {
			log.Printf("warning: could not close DB: %v", err)
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

	resp, err := http.DefaultClient.Do(req)
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
			log.Printf("warning: could not close DB: %v", err)
		}
	}(gz)

	mmdb, err := extractMMDB(editionID, gz)
	if err != nil {
		return err
	}

	// Write to a temp file in the same directory, then rename atomically
	// so in-flight requests always see a complete file.
	return saveAtomic(destPath, mmdb)
}

func extractMMDB(editionID string, gz *gzip.Reader) ([]byte, error) {
	tr := tar.NewReader(gz)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("tar %s: %w", editionID, err)
		}
		if hdr.Typeflag != tar.TypeReg || !strings.HasSuffix(hdr.Name, ".mmdb") {
			continue
		}
		data, err := io.ReadAll(tr)
		if err != nil {
			return nil, fmt.Errorf("extract %s: %w", editionID, err)
		}
		return data, nil
	}
	return nil, fmt.Errorf("no .mmdb found in %s archive", editionID)
}

func saveAtomic(destPath string, data []byte) error {
	dir := filepath.Dir(destPath)
	tmp, err := os.CreateTemp(dir, ".mmdb-download-*")
	if err != nil {
		return fmt.Errorf("temp file: %w", err)
	}
	tmpName := tmp.Name()

	if _, err := tmp.Write(data); err != nil {
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
