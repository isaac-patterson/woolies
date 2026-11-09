package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func tmpCupboard(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "c.csv")
	if body != "" {
		os.WriteFile(p, []byte(body), 0o644)
	}
	return p
}

func runCup(p string, o opts, pos ...string) (obj, error) {
	o["file"] = p
	return cmdCupboardFile(pos[0], pos, o)
}

const base = newHeader +
	"144329,Onion Brown,3,order 1,2026-11-01,,,,\n" +
	"144497,Onion Red,1,order 1,2026-11-01,,,,\n" +
	"38963,Passata,2,order 1,2026-11-01,700g,700,g,\n"

func read(p string) string { b, _ := os.ReadFile(p); return string(b) }

func TestCupboardList(t *testing.T) {
	p := tmpCupboard(t, base)
	r, err := runCup(p, opts{}, "list")
	if err != nil || r["total"] != 3 || r["count"] != 3 {
		t.Fatal(r, err)
	}
	r, _ = runCup(p, opts{"search": "ONION"}, "list")
	if r["count"] != 2 || r["total"] != 3 {
		t.Fatal(r)
	}
	if _, err := runCup(filepath.Join(t.TempDir(), "none.csv"), opts{}, "list"); err != nil {
		t.Fatal(err)
	}
}

func TestCupboardAddNewAndMerge(t *testing.T) {
	p := tmpCupboard(t, base)
	r, err := runCup(p, opts{"qty": "2", "size": "1kg", "source": "elsewhere"}, "add", "Brown", "Rice")
	if err != nil || r["action"] != "added" {
		t.Fatal(r, err)
	}
	if !strings.Contains(read(p), ",Brown Rice,2,elsewhere,") || !strings.Contains(read(p), ",1kg,1000,g,") {
		t.Fatalf("%s", read(p))
	}
	if read(p+".bak") != base {
		t.Fatal("bad backup")
	}
	// merge by name (case-insensitive)
	r, err = runCup(p, opts{}, "add", "onion brown")
	if err != nil || r["action"] != "merged" || !strings.Contains(read(p), "144329,Onion Brown,4,") {
		t.Fatal(r, err, read(p))
	}
	// merge by stockcode
	r, _ = runCup(p, opts{"stockcode": "38963", "qty": "3"}, "add", "Pasta sauce")
	if r["action"] != "merged" || !strings.Contains(read(p), "38963,Passata,5,") {
		t.Fatal(r, read(p))
	}
	if _, err := runCup(p, opts{"size": "lots"}, "add", "x"); !isUsage(err) {
		t.Fatal("want usage error", err)
	}
	if _, err := runCup(p, opts{"qty": "0"}, "add", "x"); !isUsage(err) {
		t.Fatal("want usage error", err)
	}
}

func isUsage(err error) bool { var ue *usageError; return errors.As(err, &ue) }

func TestCupboardUseRemoveSetQty(t *testing.T) {
	p := tmpCupboard(t, base)
	if _, err := runCup(p, opts{}, "use", "onion"); err == nil || !strings.Contains(err.Error(), "matches 2 rows") {
		t.Fatal("want ambiguity", err)
	}
	if read(p) != base {
		t.Fatal("changed on ambiguity")
	}
	if _, err := runCup(p, opts{}, "use", "Onion Brown"); err != nil || !strings.Contains(read(p), "Onion Brown,2,") {
		t.Fatal(err, read(p))
	}
	r, _ := runCup(p, opts{"qty": "5"}, "use", "144497")
	if r["action"] != "used-up" || strings.Contains(read(p), "Onion Red") {
		t.Fatal(r, read(p))
	}
	if _, err := runCup(p, opts{}, "set-qty", "passata", "9"); err != nil || !strings.Contains(read(p), "Passata,9,") {
		t.Fatal(err, read(p))
	}
	if _, err := runCup(p, opts{}, "set-qty", "Passata", "x"); !isUsage(err) {
		t.Fatal(err)
	}
	if _, err := runCup(p, opts{}, "remove", "38963"); err != nil || strings.Contains(read(p), "Passata") {
		t.Fatal(err)
	}
	if _, err := runCup(p, opts{}, "remove", "nothing here"); err == nil || isUsage(err) {
		t.Fatal("want not-found error", err)
	}
}

func TestCupboardDryRunWritesNothing(t *testing.T) {
	p := tmpCupboard(t, base)
	for _, c := range [][]string{{"use", "Passata"}, {"remove", "Passata"}, {"set-qty", "Passata", "7"}, {"add", "Salt"}, {"clear"}} {
		if _, err := runCup(p, opts{"dry-run": "true"}, c...); err != nil {
			t.Fatal(c, err)
		}
	}
	if read(p) != base {
		t.Fatal("dry run wrote")
	}
	if _, err := os.Stat(p + ".bak"); err == nil {
		t.Fatal("dry run backed up")
	}
}

func TestCupboardClear(t *testing.T) {
	p := tmpCupboard(t, base)
	if _, err := runCup(p, opts{}, "clear"); !isUsage(err) || read(p) != base {
		t.Fatal("clear without --yes should refuse", err)
	}
	r, err := runCup(p, opts{"yes": "true"}, "clear")
	if err != nil || r["cleared"] != 3 || read(p) != newHeader || read(p+".bak") != base {
		t.Fatal(r, err, read(p))
	}
}

func TestCupboardOldFormatUpgradedOnWrite(t *testing.T) {
	old := "stockcode,item,qty,source,confirmed\n1,Milk,1,had,2026-01-01\n"
	p := tmpCupboard(t, old)
	if _, err := runCup(p, opts{}, "set-qty", "Milk", "4"); err != nil {
		t.Fatal(err)
	}
	if read(p) != sizedHeader+"1,Milk,4,had,2026-01-01,,,,\n" || read(p+".bak") != old {
		t.Fatal(read(p))
	}
}

func TestCupboardFileOnly(t *testing.T) {
	if cupboardFileOnly([]string{"add-latest"}) || !cupboardFileOnly([]string{"list"}) || !cupboardFileOnly(nil) {
		t.Fatal("wrong browser/no-browser split")
	}
}
