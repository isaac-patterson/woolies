package main

import (
	"os"
	"strings"
	"testing"
	"time"
)

func TestPageSizeArg(t *testing.T) {
	tests := []struct {
		name    string
		o       opts
		want    int
		warns   bool
		wantErr bool
	}{
		{"default", opts{}, 8, false, false},
		{"at the limit", opts{"page-size": "36"}, 36, false, false},
		{"over the limit", opts{"page-size": "100"}, 36, true, false},
		{"zero", opts{"page-size": "0"}, 0, false, true},
		{"negative", opts{"page-size": "-1"}, 0, false, true},
		{"not a number", opts{"page-size": "lots"}, 0, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, warning, err := pageSizeArg(tt.o)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want || (warning != "") != tt.warns {
				t.Errorf("got (%d, %q), want size %d, warns %v", got, warning, tt.want, tt.warns)
			}
		})
	}
}

func TestLastWeeklyRollover(t *testing.T) {
	loc := time.FixedZone("AEST", 10*60*60)
	wed := time.Date(2026, 11, 4, 0, 0, 0, 0, loc)
	tests := []struct {
		name string
		now  time.Time
		want time.Time
	}{
		{"Wednesday midnight", wed, wed},
		{"later on Wednesday", wed.Add(15 * time.Hour), wed},
		{"Saturday", time.Date(2026, 11, 7, 9, 0, 0, 0, loc), wed},
		{"Tuesday just before the change", wed.Add(7*24*time.Hour - time.Minute), wed},
		{"across a month start", time.Date(2026, 12, 1, 9, 0, 0, 0, loc), time.Date(2026, 11, 25, 0, 0, 0, 0, loc)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := lastWeeklyRollover(tt.now); !got.Equal(tt.want) {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCacheFresh(t *testing.T) {
	loc := time.FixedZone("AEST", 10*60*60)
	sat := time.Date(2026, 11, 7, 12, 0, 0, 0, loc)
	day := 24 * time.Hour
	tests := []struct {
		name    string
		fetched time.Time
		now     time.Time
		maxAge  time.Duration
		want    bool
	}{
		{"just fetched", sat, sat, day, true},
		{"within max age", sat.Add(-23 * time.Hour), sat, day, true},
		{"at max age", sat.Add(-day), sat, day, false},
		{"zero max age always fetches", sat, sat, 0, false},
		{"fetched in the future", sat.Add(time.Hour), sat, day, false},
		{"crossed Wednesday within max age", time.Date(2026, 11, 3, 23, 0, 0, 0, loc), time.Date(2026, 11, 4, 1, 0, 0, 0, loc), day, false},
		{"long max age still stops at Wednesday", time.Date(2026, 11, 3, 12, 0, 0, 0, loc), sat, 30 * day, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := cacheFresh(tt.fetched, tt.now, tt.maxAge); got != tt.want {
				t.Errorf("got %v, want %v", got, tt.want)
			}
		})
	}
}

func TestCacheable(t *testing.T) {
	deals := []obj{{"stockcode": 1.0}}
	if !cacheable(deals, []any{obj{"name": "A"}}, nil) {
		t.Error("a complete run should be cacheable")
	}
	if cacheable(nil, nil, nil) {
		t.Error("an empty run should not be cacheable")
	}
	if cacheable(deals, nil, []string{"A page 2: boom"}) {
		t.Error("a run with errors should not be cacheable")
	}
	if cacheable(deals, []any{obj{"name": "A", "truncated": true}}, nil) {
		t.Error("a truncated run should not be cacheable")
	}
}

// saveDeals writes a cache built the way a live run builds one.
func saveDeals(t *testing.T, fetchedAt time.Time) {
	t.Helper()
	meat, ok := trimSpecial(raw(1, "Mince", obj{
		"CentreTag":            obj{"TagContentText": "Was $20 - 04/10/2026"},
		"AdditionalAttributes": obj{"piesdepartmentnamesjson": `["Poultry, Meat & Seafood"]`},
	}))
	if !ok {
		t.Fatal("mince should be a deal")
	}
	soap, ok := trimSpecial(raw(2, "Soap", obj{
		"IsOnSpecial": true, "WasPrice": 40.0,
		"AdditionalAttributes": obj{"piesdepartmentnamesjson": `["Personal Care"]`},
	}))
	if !ok {
		t.Fatal("soap should be a deal")
	}
	c := &specialsCache{FetchedAt: fetchedAt, StoreID: 1234.0, Lists: []any{obj{"name": "Half Price"}}, Deals: []obj{meat, soap}}
	if err := saveSpecialsCache(c); err != nil {
		t.Fatal(err)
	}
}

// A fresh cache must answer without a browser, and a deal read back from JSON must still work with
// the filter and sort, which expect int and []string.
func TestSpecialsAllFromCache(t *testing.T) {
	t.Setenv("WOOLIES_HOME", t.TempDir())
	saveDeals(t, time.Now().Add(-time.Minute))

	out, err := cmdSpecialsAll(opts{})
	if err != nil {
		t.Fatal(err)
	}
	if asObj(out["cache"])["hit"] != true {
		t.Fatalf("cache = %v, want a hit", out["cache"])
	}
	if out["storeId"] != 1234.0 || out["totalDeals"] != 2 {
		t.Errorf("storeId = %v, totalDeals = %v", out["storeId"], out["totalDeals"])
	}
	products := out["products"].([]any)
	if len(products) != 2 || products[0].(obj)["displayName"] != "Soap" {
		t.Errorf("products = %v, want Soap (75%%) first", products)
	}

	out, err = cmdSpecialsAll(opts{"food": "true", "department": "meat", "min-discount": "50"})
	if err != nil {
		t.Fatal(err)
	}
	if out["matched"] != 1 || out["products"].([]any)[0].(obj)["displayName"] != "Mince" {
		t.Errorf("filtered = %v", out["products"])
	}
}

func TestLoadSpecialsCacheRejectsBadFiles(t *testing.T) {
	t.Setenv("WOOLIES_HOME", t.TempDir())
	if _, err := loadSpecialsCache(); err == nil {
		t.Error("a missing file should be an error")
	}
	for name, body := range map[string]string{
		"corrupt":      `{"version":1,"deals":[`,
		"old version":  `{"version":0,"deals":[]}`,
		"missing data": `{"version":1,"deals":[{"displayName":"x"}]}`,
	} {
		if err := os.WriteFile(specialsCachePath(), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadSpecialsCache(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestSaveSpecialsCacheLeavesNoTempFiles(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("WOOLIES_HOME", dir)
	saveDeals(t, time.Now())
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("left behind %s", e.Name())
		}
	}
	info, err := os.Stat(specialsCachePath())
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v, want 0600", info.Mode().Perm())
	}
}
