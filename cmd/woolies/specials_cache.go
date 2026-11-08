package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	specialsCacheVersion = 1 // bump when the shape of a cached deal changes
	defaultCacheMaxHours = 24
)

// specialsCache is the full, unfiltered deal list, so any filter can be answered without the site.
type specialsCache struct {
	Version   int       `json:"version"`
	FetchedAt time.Time `json:"fetchedAt"`
	StoreID   any       `json:"storeId"`
	Lists     []any     `json:"lists"`
	Deals     []obj     `json:"deals"`
}

func specialsCachePath() string { return filepath.Join(dataDir(), "specials-cache.json") }

// loadSpecialsCache reads the cache. Any error just means the caller should fetch live.
func loadSpecialsCache() (*specialsCache, error) {
	b, err := os.ReadFile(specialsCachePath())
	if err != nil {
		return nil, err
	}
	var c specialsCache
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, err
	}
	if c.Version != specialsCacheVersion {
		return nil, fmt.Errorf("cache version %d, want %d", c.Version, specialsCacheVersion)
	}
	// JSON brings these back as float64 and []any, but the filter and sort expect int and []string.
	for _, d := range c.Deals {
		percent, ok := d["discountPercent"].(float64)
		if !ok {
			return nil, fmt.Errorf("cached deal has no discountPercent")
		}
		d["discountPercent"] = int(percent)
		d["departments"] = stringList(d["departments"])
		d["categories"] = stringList(d["categories"])
	}
	return &c, nil
}

// saveSpecialsCache writes through a temp file, so a crash or a Ctrl-C can't leave half a cache.
func saveSpecialsCache(c *specialsCache) error {
	c.Version = specialsCacheVersion
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dataDir(), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(dataDir(), "specials-cache-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err := f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), specialsCachePath())
}

// lastWeeklyRollover is the most recent local Wednesday midnight, when weekly specials change over.
func lastWeeklyRollover(now time.Time) time.Time {
	back := (int(now.Weekday()) - int(time.Wednesday) + 7) % 7
	y, m, d := now.Date()
	return time.Date(y, m, d-back, 0, 0, 0, 0, now.Location())
}

// cacheFresh reports whether a cache fetched at fetchedAt can still be used at now.
func cacheFresh(fetchedAt, now time.Time, maxAge time.Duration) bool {
	age := now.Sub(fetchedAt)
	return age >= 0 && age < maxAge && !fetchedAt.Before(lastWeeklyRollover(now))
}

// cacheable reports whether a live run saw everything. A partial run must not hide the missing
// deals from later calls.
func cacheable(deals []obj, reports []any, errs []string) bool {
	if len(deals) == 0 || len(errs) > 0 {
		return false
	}
	for _, r := range reports {
		if asObj(r)["truncated"] == true {
			return false
		}
	}
	return true
}
