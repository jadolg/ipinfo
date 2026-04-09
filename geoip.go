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
	"sync"
	"time"

	"github.com/oschwald/geoip2-golang"
)

type server struct {
	mu     sync.RWMutex
	cityDB *geoip2.Reader
	asnDB  *geoip2.Reader
	tor    *torExitSet
}

func (s *server) getCityDB() *geoip2.Reader {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cityDB
}

func (s *server) getAsnDB() *geoip2.Reader {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.asnDB
}

func downloadDB(editionID, accountID, licenseKey, destPath string) (*geoip2.Reader, error) {
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
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			log.Printf("warning: could not close response body %q: %v", editionID, err)
		}
	}(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download %s: HTTP %d", editionID, resp.StatusCode)
	}

	gz, err := gzip.NewReader(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("gzip %s: %w", editionID, err)
	}
	defer func(gz *gzip.Reader) {
		err := gz.Close()
		if err != nil {
			log.Printf("warning: could not close gzip reader %q: %v", editionID, err)
		}
	}(gz)

	// Write the extracted .mmdb to a temp file in the same directory, then
	// rename atomically so in-flight requests always see a complete file.
	dir := filepath.Dir(destPath)
	tmp, err := os.CreateTemp(dir, ".mmdb-download-*")
	if err != nil {
		return nil, fmt.Errorf("temp file: %w", err)
	}
	tmpName := tmp.Name()
	renamed := false
	defer func() {
		if renamed {
			return
		}
		tmp.Close()
		os.Remove(tmpName)
	}()

	tr := tar.NewReader(gz)
	found := false
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
		if _, err := io.Copy(tmp, tr); err != nil {
			return nil, fmt.Errorf("extract %s: %w", editionID, err)
		}
		found = true
		break
	}
	if !found {
		return nil, fmt.Errorf("no .mmdb found in %s archive", editionID)
	}
	if err := tmp.Close(); err != nil {
		return nil, err
	}
	if err := os.Rename(tmpName, destPath); err != nil {
		return nil, fmt.Errorf("rename %s: %w", editionID, err)
	}
	renamed = true

	return geoip2.Open(destPath)
}

func (s *server) refreshDBs(accountID, licenseKey, cityPath, asnPath string) {
	cityDB, err := downloadDB("GeoLite2-City", accountID, licenseKey, cityPath)
	if err != nil {
		log.Printf("GeoLite2-City refresh failed: %v", err)
	}

	asnDB, err := downloadDB("GeoLite2-ASN", accountID, licenseKey, asnPath)
	if err != nil {
		log.Printf("GeoLite2-ASN refresh failed: %v", err)
	}

	s.mu.Lock()
	oldCity := s.cityDB
	oldASN := s.asnDB
	if cityDB != nil {
		s.cityDB = cityDB
	}
	if asnDB != nil {
		s.asnDB = asnDB
	}
	s.mu.Unlock()

	if oldCity != nil && cityDB != nil {
		err := oldCity.Close()
		if err != nil {
			log.Println("warning: could not close old city DB")
			return
		}
	}
	if oldASN != nil && asnDB != nil {
		err := oldASN.Close()
		if err != nil {
			log.Println("warning: could not close old ASN DB")
			return
		}
	}
	log.Printf("GeoIP databases refreshed")
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
