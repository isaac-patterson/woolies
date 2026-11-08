package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testOrder() obj {
	return obj{"orderId": float64(42), "items": []any{
		obj{"stockcode": float64(1), "name": `Smith's "Chips", 200g`, "qty": float64(2)},
		obj{"stockcode": float64(2), "name": "Milk", "qty": float64(1)},
	}}
}

func fakeLookup(code string) sizeInfo {
	switch code {
	case "1":
		return sizeInfo{Size: "200g", Qty: "200", Unit: "g", Storage: "Cool, dry place"}
	case "2":
		return sizeInfo{Size: "2L", Qty: "2000", Unit: "ml"}
	}
	return sizeInfo{}
}

const newHeader = "stockcode,item,qty,source,confirmed,size,size_qty,size_unit,storage\n"

func TestApplyCupboardCreatesAndIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "c.csv")
	res, err := applyCupboard(p, testOrder(), "2026-11-07", false, fakeLookup)
	if err != nil || len(res.added) != 2 || len(res.skipped) != 0 || res.migrated {
		t.Fatalf("first run: %+v %v", res, err)
	}
	b, _ := os.ReadFile(p)
	want := newHeader + "1,\"Smith's \"\"Chips\"\", 200g\",2,order 42,2026-11-07,200g,200,g,\"Cool, dry place\"\n2,Milk,1,order 42,2026-11-07,2L,2000,ml,\n"
	if string(b) != want {
		t.Fatalf("file:\n%s\nwant:\n%s", b, want)
	}
	res, err = applyCupboard(p, testOrder(), "2026-11-08", false, fakeLookup)
	if err != nil || len(res.added) != 0 || len(res.skipped) != 2 {
		t.Fatalf("second run: %+v %v", res, err)
	}
	if b2, _ := os.ReadFile(p); string(b2) != want {
		t.Fatal("file changed on re-run")
	}
}

func TestApplyCupboardMigratesOldFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.csv")
	old := "stockcode,item,qty,source,confirmed\n1,Chips,2,order 42,2026-11-01\n9,Rice,1,had,2026-01-01"
	os.WriteFile(p, []byte(old), 0o644)

	dry, err := applyCupboard(p, testOrder(), "2026-11-07", true, fakeLookup)
	if err != nil || !dry.migrated || len(dry.backfilled) != 2 || len(dry.added) != 1 || len(dry.skipped) != 1 {
		t.Fatalf("dry: %+v %v", dry, err)
	}
	if b, _ := os.ReadFile(p); string(b) != old {
		t.Fatal("dry run modified file")
	}
	if _, err := os.Stat(p + ".bak"); err == nil {
		t.Fatal("dry run made a backup")
	}

	if _, err := applyCupboard(p, testOrder(), "2026-11-07", false, fakeLookup); err != nil {
		t.Fatal(err)
	}
	want := newHeader + "1,Chips,2,order 42,2026-11-01,200g,200,g,\"Cool, dry place\"\n9,Rice,1,had,2026-01-01,,,,\n2,Milk,1,order 42,2026-11-07,2L,2000,ml,\n"
	if b, _ := os.ReadFile(p); string(b) != want {
		t.Fatalf("migrated:\n%s", b)
	}
	if b, _ := os.ReadFile(p + ".bak"); string(b) != old {
		t.Fatalf("bad backup: %q", b)
	}
}

func TestApplyCupboardDryRunMissingFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.csv")
	res, err := applyCupboard(p, testOrder(), "d", true, fakeLookup)
	if err != nil || len(res.added) != 2 {
		t.Fatal(res, err)
	}
	if _, err := os.Stat(p); err == nil {
		t.Fatal("dry run created file")
	}
}

func TestApplyCupboardAppendMissingNewline(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.csv")
	os.WriteFile(p, []byte(newHeader+"1,Milk,1,had,2026-01-01,,,,"), 0o644)
	res, err := applyCupboard(p, testOrder(), "d", false, fakeLookup)
	if err != nil || len(res.added) != 2 || res.migrated {
		t.Fatal(res, err)
	}
	b, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(b), newHeader+"1,Milk,1,had,2026-01-01,,,,\n1,") {
		t.Fatalf("bad join: %q", b)
	}
}

func TestParsePackSize(t *testing.T) {
	cases := []struct {
		in string
		q  float64
		u  string
		ok bool
	}{
		{"700g", 700, "g", true}, {"1kg", 1000, "g", true}, {"1.5 KG", 1500, "g", true},
		{"1L", 1000, "ml", true}, {"375mL", 375, "ml", true}, {"each", 1, "each", true},
		{"6pk", 6, "each", true}, {"4 x 125g", 500, "g", true}, {"per kg", 0, "", false}, {"", 0, "", false},
	}
	for _, c := range cases {
		q, u, ok := parsePackSize(c.in)
		if q != c.q || u != c.u || ok != c.ok {
			t.Errorf("%q: got %v %v %v", c.in, q, u, ok)
		}
	}
}

func TestSizeFromProduct(t *testing.T) {
	d := obj{
		"Product":              obj{"PackageSize": "700g"},
		"AdditionalAttributes": obj{"storageinstructions": "Keep refrigerated."},
	}
	if i := sizeFromProduct(d); i.Size != "700g" || i.Qty != "700" || i.Unit != "g" || i.Storage != "Keep refrigerated." {
		t.Fatalf("%+v", i)
	}
	loose := obj{"Product": obj{"PackageSize": "each", "UnitWeightInGrams": float64(175)},
		"AdditionalAttributes": obj{"storageinstructions": "Stockroom Storage & Ambient Display"}}
	if i := sizeFromProduct(loose); i.Unit != "each" || i.Qty != "1" || i.Storage != "" {
		t.Fatalf("%+v", i)
	}
	cool := obj{"Product": obj{"PackageSize": "each"}, "AdditionalAttributes": obj{"storageinstructions": "Coolroom Storage & Chilled Display"}}
	if i := sizeFromProduct(cool); i.Storage != "" {
		t.Fatalf("%+v", i)
	}
	nut := obj{"Product": obj{"PackageSize": "Per kg"},
		"AdditionalAttributes": obj{"servingsperpack-total-nip": "5.0", "servingsize-total-nip": "30.0g"}}
	if i := sizeFromProduct(nut); i.Qty != "150" || i.Unit != "g" || i.Source != "nutrition" {
		t.Fatalf("%+v", i)
	}
	if i := sizeFromProduct(obj{"Product": obj{"PackageSize": "Per kg"}}); i.Size != "" || i.Unit != "" {
		t.Fatalf("%+v", i)
	}
}
