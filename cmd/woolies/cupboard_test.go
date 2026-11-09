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
		return sizeInfo{Size: "200g", Qty: "200", Unit: "g", Storage: "Cool, dry place",
			Nut: [8]string{"1530", "12.5", "2", "1", "72", "2.5", "3", "30"}}
	case "2":
		return sizeInfo{Size: "2L", Qty: "2000", Unit: "ml"}
	}
	return sizeInfo{}
}

// sizedHeader is the previous 9-column format (sizes, no nutrition); newHeader is the current one.
const (
	sizedHeader = "stockcode,item,qty,source,confirmed,size,size_qty,size_unit,storage\n"
	newHeader   = "stockcode,item,qty,source,confirmed,size,size_qty,size_unit,storage,kj,protein_g,fat_g,sat_fat_g,carb_g,sugars_g,fibre_g,sodium_mg\n"
)

func TestApplyCupboardCreatesAndIsIdempotent(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sub", "c.csv")
	res, err := applyCupboard(p, testOrder(), "2026-11-07", false, fakeLookup)
	if err != nil || len(res.added) != 2 || len(res.skipped) != 0 || res.migrated {
		t.Fatalf("first run: %+v %v", res, err)
	}
	b, _ := os.ReadFile(p)
	want := newHeader + "1,\"Smith's \"\"Chips\"\", 200g\",2,order 42,2026-11-07,200g,200,g,\"Cool, dry place\",1530,12.5,2,1,72,2.5,3,30\n2,Milk,1,order 42,2026-11-07,2L,2000,ml,,,,,,,,,\n"
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
	want := newHeader + "1,Chips,2,order 42,2026-11-01,200g,200,g,\"Cool, dry place\",1530,12.5,2,1,72,2.5,3,30\n9,Rice,1,had,2026-01-01,,,,,,,,,,,,\n2,Milk,1,order 42,2026-11-07,2L,2000,ml,,,,,,,,,\n"
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
	os.WriteFile(p, []byte(newHeader+"1,Milk,1,had,2026-01-01,,,,,,,,,,,,"), 0o644)
	res, err := applyCupboard(p, testOrder(), "d", false, fakeLookup)
	if err != nil || len(res.added) != 2 || res.migrated {
		t.Fatal(res, err)
	}
	b, _ := os.ReadFile(p)
	if !strings.HasPrefix(string(b), newHeader+"1,Milk,1,had,2026-01-01,,,,,,,,,,,,\n1,") {
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

func TestApplyCupboardMigratesSizedFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.csv")
	old := sizedHeader + "1,Chips,2,order 42,2026-11-01,hand-edited,999,g,Mine\n,Loose item,1,had,2026-01-01,,,,\n"
	os.WriteFile(p, []byte(old), 0o644)
	var looked []string
	lookup := func(code string) sizeInfo { looked = append(looked, code); return fakeLookup(code) }
	res, err := applyCupboard(p, obj{"orderId": float64(43), "items": []any{}}, "2026-11-09", false, lookup)
	if err != nil || !res.migrated || len(res.backfilled) != 2 {
		t.Fatalf("%+v %v", res, err)
	}
	if len(looked) != 1 || looked[0] != "1" {
		t.Fatalf("rows without a stockcode must not be looked up, got %v", looked)
	}
	// Existing size columns are kept; nutrition is filled in.
	want := newHeader + "1,Chips,2,order 42,2026-11-01,hand-edited,999,g,Mine,1530,12.5,2,1,72,2.5,3,30\n,Loose item,1,had,2026-01-01,,,,,,,,,,,,\n"
	if b, _ := os.ReadFile(p); string(b) != want {
		t.Fatalf("migrated:\n%s", b)
	}
	if b, _ := os.ReadFile(p + ".bak"); string(b) != old {
		t.Fatal("bad backup")
	}
	if res, err = applyCupboard(p, obj{"orderId": float64(43), "items": []any{}}, "d", false, lookup); err != nil || res.migrated {
		t.Fatalf("second run should not migrate: %+v %v", res, err)
	}
}

func TestApplyCupboardRejectsOddHeader(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.csv")
	os.WriteFile(p, []byte("stockcode,item,qty,source,confirmed,size,size_qty\n"), 0o644)
	if _, err := applyCupboard(p, testOrder(), "d", false, fakeLookup); err == nil {
		t.Fatal("want an error for a 7-column header")
	}
}

func TestFileOnlyWriteKeepsSizedWidth(t *testing.T) {
	p := tmpCupboard(t, sizedHeader+"1,Chips,2,order 42,2026-11-01,200g,200,g,\n")
	if _, err := runCup(p, opts{"qty": "1"}, "add", "Rice"); err != nil {
		t.Fatal(err)
	}
	if got := read(p); !strings.HasPrefix(got, sizedHeader) || strings.Contains(got, "kj") {
		t.Fatalf("a sized file must stay 9 columns until add-latest back-fills it:\n%s", got)
	}
}

func TestNutritionFromProduct(t *testing.T) {
	panel := func(name, per100, perServe string) any {
		return obj{"Name": name, "ServingSize": "100.0 G", "ServingsPerPack": "5.0",
			"Values": obj{"Quantity Per 100g / 100mL": per100, "Quantity Per Serving": perServe}}
	}
	d := obj{"NutritionalInformation": []any{
		panel("Energy", "1530.0kJ", "999kJ"),
		panel("Protein", "12.5g", "1g"),
		panel("Fat, Total", "2.0g", "1g"),
		panel("\u2013 Saturated", "< 1.0g", "1g"), // a bound is not a value
		panel("Carbohydrate", "72.0g", "1g"),
		panel("\u2013 Sugars", "2.5g", "1g"),
		panel("Dietary Fibre", "3.0g", "1g"),
		panel("Sodium", "30.0mg", "1mg"),
		panel("Vitamin C", "9g", "1g"),
	}}
	want := [8]string{"1530", "12.5", "2", "", "72", "2.5", "3", "30"}
	if got := nutritionFromProduct(d); got != want {
		t.Fatalf("got %v want %v", got, want)
	}

	// Unit conversion: kcal -> kJ, sodium in g -> mg, fat in mg -> g; wrong-kind units are dropped.
	conv := obj{"NutritionalInformation": []any{
		panel("Energy", "100kcal", ""), panel("Sodium", "0.5g", ""), panel("Protein", "500mg", ""), panel("Fat, Total", "10kJ", ""),
	}}
	if got := nutritionFromProduct(conv); got != [8]string{"418.4", "0.5", "", "", "", "", "", "500"} {
		t.Fatalf("conversions: %v", got)
	}

	// "Approx." values (common on produce and fresh food) are stated estimates and are kept.
	approx := obj{"NutritionalInformation": []any{panel("Energy", "Approx.125kJ", ""), panel("Sodium", "Approx.200mg", ""), panel("Fat, Total", "Approx. 0.1g", "")}}
	if got := nutritionFromProduct(approx); got != [8]string{"125", "", "0.1", "", "", "", "", "200"} {
		t.Fatalf("approx: %v", got)
	}

	// With no panel rows, the flat NIP attributes in the nested JSON fill in.
	flat := obj{"AdditionalAttributes": obj{"nutritionalinformation": `{"Attributes":[
		{"Name":"Energy kJ Quantity Per 100g - Total - NIP","Value":"1530.0kJ"},
		{"Name":"Sugars Quantity Per 100g - ValueWord - NIP","Value":"2.5"},
		{"Name":"Sugars Quantity Per 100g - Total - NIP","Value":"2.5g"},
		{"Name":"Sodium Quantity Per Serve - Total - NIP","Value":"1mg"}]}`}}
	if got := nutritionFromProduct(flat); got != [8]string{"1530", "", "", "", "", "2.5", "", ""} {
		t.Fatalf("flat: %v", got)
	}
	if got := nutritionFromProduct(obj{}); got != [8]string{} {
		t.Fatalf("empty: %v", got)
	}
}

// The serving-size fallback used to read only two flat attributes, which real products leave empty
// while the same numbers sit in the panel rows or the nested NIP JSON.
func TestSizeFromProductServingFallbackSources(t *testing.T) {
	rows := obj{"Product": obj{"PackageSize": "Per kg"},
		"NutritionalInformation": []any{obj{"Name": "Energy", "ServingSize": "100.0 G", "ServingsPerPack": "5.0", "Values": obj{}}},
		"AdditionalAttributes":   obj{"servingsize-total-nip": nil, "servingsperpack-total-nip": nil}}
	if i := sizeFromProduct(rows); i.Qty != "500" || i.Unit != "g" || i.Source != "nutrition" {
		t.Fatalf("panel rows: %+v", i)
	}
	nested := obj{"Product": obj{"PackageSize": "Per kg"}, "AdditionalAttributes": obj{"nutritionalinformation": `{"Attributes":[
		{"Name":"Serving Size - Total - NIP","Value":"30.0 G"},{"Name":"Servings Per Pack - Total - NIP","Value":"10.0"}]}`}}
	if i := sizeFromProduct(nested); i.Qty != "300" || i.Unit != "g" || i.Source != "nutrition" {
		t.Fatalf("nested: %+v", i)
	}
}
