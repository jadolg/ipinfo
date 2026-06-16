package main

import (
	"net"
	"os"
	"testing"
	"time"

	"github.com/oschwald/geoip2-golang"
)

const (
	testCityDBPath = "GeoLite2-City.mmdb"
	testASNDBPath  = "GeoLite2-ASN.mmdb"
)

// skipIfNoDBs skips the test if the mmdb files are not present.
func skipIfNoDBs(t testing.TB) {
	t.Helper()
	for _, path := range []string{testCityDBPath, testASNDBPath} {
		if _, err := os.Stat(path); err != nil {
			t.Skipf("mmdb file not available (%s): skipping", path)
		}
	}
}

func openTestGeoDB(t testing.TB) *geoDB {
	t.Helper()
	skipIfNoDBs(t)
	g := &geoDB{}
	g.open(testCityDBPath, testASNDBPath)
	t.Cleanup(func() {
		if r := g.cityReader(); r != nil {
			_ = r.Close()
		}
		if r := g.asnReader(); r != nil {
			_ = r.Close()
		}
	})
	return g
}

// --- reader state ---

func TestGeoDBReadersNilWhenEmpty(t *testing.T) {
	g := &geoDB{}
	if g.cityReader() != nil {
		t.Error("cityReader should be nil on a fresh geoDB")
	}
	if g.asnReader() != nil {
		t.Error("asnReader should be nil on a fresh geoDB")
	}
}

func TestGeoDBOpenSetsReaders(t *testing.T) {
	g := openTestGeoDB(t)

	if g.cityReader() == nil {
		t.Error("cityReader should not be nil after open")
	}
	if g.asnReader() == nil {
		t.Error("asnReader should not be nil after open")
	}
}

func TestGeoDBStoreReplacesReader(t *testing.T) {
	skipIfNoDBs(t)

	db1, err := geoip2.Open(testCityDBPath)
	if err != nil {
		t.Fatalf("open db1: %v", err)
	}
	db2, err := geoip2.Open(testCityDBPath)
	if err != nil {
		_ = db1.Close()
		t.Fatalf("open db2: %v", err)
	}
	t.Cleanup(func() { _ = db2.Close() })

	g := &geoDB{}
	g.storeCity(db1)
	if g.cityReader() != db1 {
		t.Fatal("cityReader should return db1 after first store")
	}

	// storing db2 should swap out and close db1
	g.storeCity(db2)
	if g.cityReader() != db2 {
		t.Fatal("cityReader should return db2 after second store")
	}
}

// --- lookups ---

func TestGeoDBCityLookup(t *testing.T) {
	g := openTestGeoDB(t)

	rec, err := g.cityReader().City(net.ParseIP("8.8.8.8"))
	if err != nil {
		t.Fatalf("city lookup: %v", err)
	}
	if rec.Country.IsoCode == "" {
		t.Error("expected non-empty country code for 8.8.8.8")
	}
}

func TestGeoDBASNLookup(t *testing.T) {
	g := openTestGeoDB(t)

	rec, err := g.asnReader().ASN(net.ParseIP("8.8.8.8"))
	if err != nil {
		t.Fatalf("ASN lookup: %v", err)
	}
	if rec.AutonomousSystemOrganization == "" {
		t.Error("expected non-empty ASN organization for 8.8.8.8")
	}
}

// --- dbsNeedRefresh ---

func TestDBsNeedRefreshMissingFile(t *testing.T) {
	if !dbsNeedRefresh("/nonexistent/a.mmdb", "/nonexistent/b.mmdb", time.Hour) {
		t.Error("should need refresh when files do not exist")
	}
}

func TestDBNeedsRefreshInvalidFile(t *testing.T) {
	// A file that is not a valid mmdb (e.g. an empty placeholder left by a
	// broken download) cannot have its build epoch read and must refresh.
	if !dbNeedsRefresh(createTempFile(t), time.Hour) {
		t.Error("should need refresh when file is not a valid mmdb")
	}
}

func TestDBsNeedRefreshFreshByBuildEpoch(t *testing.T) {
	skipIfNoDBs(t)
	// A huge interval makes the real DB's build date look fresh regardless of
	// how old the checked-in copy is.
	if dbsNeedRefresh(testCityDBPath, testASNDBPath, 100*365*24*time.Hour) {
		t.Error("should not need refresh when build epoch is within the interval")
	}
}

func TestDBsNeedRefreshStaleByBuildEpoch(t *testing.T) {
	skipIfNoDBs(t)
	// The build epoch is necessarily in the past, so any positive interval this
	// small marks the DB stale. This is the case the mtime-based check missed
	// when a restored volume carried a recent mtime over stale content.
	if !dbsNeedRefresh(testCityDBPath, testASNDBPath, time.Nanosecond) {
		t.Error("should need refresh when build epoch is older than the interval")
	}
}

func createTempFile(t *testing.T) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "*.mmdb")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	_ = f.Close()
	return f.Name()
}
