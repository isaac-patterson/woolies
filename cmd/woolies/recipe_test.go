package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// copyRecipes copies the fixture recipes (real files from the recipes folder) into a temp dir.
func copyRecipes(t *testing.T) string {
	dir := t.TempDir()
	ents, _ := os.ReadDir("testdata/recipes")
	for _, e := range ents {
		b, _ := os.ReadFile(filepath.Join("testdata/recipes", e.Name()))
		os.WriteFile(filepath.Join(dir, e.Name()), b, 0o644)
	}
	return dir
}

func rec(t *testing.T, dir string, pos ...string) obj {
	t.Helper()
	r, err := cmdRecipe(pos, opts{"dir": dir})
	if err != nil {
		t.Fatal(pos, err)
	}
	return r
}

func TestRecipeList(t *testing.T) {
	r := rec(t, copyRecipes(t), "list")
	if r["count"] != 3 {
		t.Fatal(r)
	}
	first := r["recipes"].([]any)[0].(obj)
	if first["slug"] != "cheesy-tomato-pasta-bake" || first["title"] != "Cheesy Tomato Pasta Bake" || first["serves"] != 4 || first["cost_per_serving"] != 5.86 {
		t.Fatal(first)
	}
}

func TestRecipeShowParsesPastaBake(t *testing.T) {
	dir := copyRecipes(t)
	r := rec(t, dir, "show", "pasta bake")
	if r["slug"] != "cheesy-tomato-pasta-bake" || r["matchedBy"] != "partial" {
		t.Fatal(r["slug"], r["matchedBy"])
	}
	if len(r["ingredients"].([]any)) != 11 || len(r["method"].([]any)) != 7 || len(r["notes"].([]any)) != 2 {
		t.Fatal("counts", len(r["ingredients"].([]any)), len(r["method"].([]any)))
	}
	if r["method"].([]any)[6].(obj)["n"] != 7 {
		t.Fatal(r["method"])
	}
	raw, _ := os.ReadFile(filepath.Join(dir, "cheesy-tomato-pasta-bake.md"))
	if r["raw"] != string(raw) {
		t.Fatal("raw differs")
	}
	fm := r["frontmatter"].(obj)
	if fm["serves"] != 4 || fm["total_cost"] != 23.45 || fm["cost_calculated"] != "2026-11-07" || fm["cost_basis"] != "share of pack used" {
		t.Fatal(fm)
	}
	shop := r["shoppingList"].([]any)
	if len(shop) != 9 {
		t.Fatal(len(shop))
	}
	passata := shop[1].(obj)
	if passata["stockcode"] != "38963" || passata["quantity"] != 2 || passata["name"] != "Passata" {
		t.Fatal(passata)
	}
	garlic := shop[4].(obj)
	if garlic["stockcode"] != "713429" || garlic["quantity"] != nil {
		t.Fatal(garlic)
	}
	home := shop[8].(obj)
	if home["stockcode"] != nil {
		t.Fatal(home)
	}
	cost := r["costBreakdown"].(obj)
	if cost["total"] != 23.45 || cost["perServing"] != 5.86 || len(cost["items"].([]any)) != 8 || len(cost["notes"].([]any)) != 2 {
		t.Fatal(cost)
	}
	item := cost["items"].([]any)[0].(obj)
	if item["name"] != "Penne 500g" || item["detail"] != "500g of 500g" || item["cost"] != 0.9 {
		t.Fatal(item)
	}
}

func TestRecipeShowPriceInShoppingLine(t *testing.T) {
	r := rec(t, copyRecipes(t), "show", "roasted-veg-ricotta-bean-sauce")
	shop := r["shoppingList"].([]any)
	ric := shop[0].(obj)
	if ric["stockcode"] != "474591" || ric["price"] != 3.6 {
		t.Fatal(ric)
	}
	if shop[3].(obj)["stockcode"] != nil {
		t.Fatal(shop[3])
	}
	if r["title"] != "Roasted Veg, Ricotta and White Bean Pasta Sauce" {
		t.Fatal(r["title"])
	}
}

func TestRecipeFuzzyAndAmbiguous(t *testing.T) {
	dir := copyRecipes(t)
	for _, k := range []string{"lemon chicken", "Sheet-Pan Lemon Chicken and Veg", "sheet-pan-lemon-chicken-veg"} {
		if r := rec(t, dir, "show", k); r["slug"] != "sheet-pan-lemon-chicken-veg" {
			t.Fatal(k, r["slug"])
		}
	}
	_, err := cmdRecipe([]string{"show", "pasta"}, opts{"dir": dir})
	if err == nil || !strings.Contains(err.Error(), "cheesy-tomato-pasta-bake") || !strings.Contains(err.Error(), "roasted-veg-ricotta-bean-sauce") {
		t.Fatal(err)
	}
	if _, err := cmdRecipe([]string{"show", "curry"}, opts{"dir": dir}); err == nil {
		t.Fatal("want no match error")
	}
}

func TestRecipeSetServes(t *testing.T) {
	dir := copyRecipes(t)
	p := filepath.Join(dir, "cheesy-tomato-pasta-bake.md")
	before, _ := os.ReadFile(p)

	r, err := cmdRecipe([]string{"set", "pasta bake"}, opts{"dir": dir, "serves": "6", "dry-run": "true"})
	if err != nil || r["dryRun"] != true || r["changed"] != true {
		t.Fatal(r, err)
	}
	if now, _ := os.ReadFile(p); string(now) != string(before) {
		t.Fatal("dry run wrote")
	}

	r, err = cmdRecipe([]string{"set", "pasta bake"}, opts{"dir": dir, "serves": "6"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r["notes"].([]any)) != 1 { // cost_per_serving not recomputed
		t.Fatal(r["notes"])
	}
	after, _ := os.ReadFile(p)
	want := strings.Replace(string(before), "serves: 4\n", "serves: 6\n", 1)
	want = strings.Replace(want, "Serves 4 generous", "Serves 6 generous", 1)
	if string(after) != want {
		t.Fatalf("unexpected rewrite:\n%s", after)
	}
	if bak, _ := os.ReadFile(p + ".bak"); string(bak) != string(before) {
		t.Fatal("bad backup")
	}
	if _, err := os.Stat(p + ".tmp"); err == nil {
		t.Fatal("temp file left behind")
	}
	if got := rec(t, dir, "show", "pasta bake"); got["serves"] != 6 {
		t.Fatal(got["serves"])
	}
}

func TestRecipeSetCostsAndNewField(t *testing.T) {
	dir := copyRecipes(t)
	p := filepath.Join(dir, "sheet-pan-lemon-chicken-veg.md")
	_, err := cmdRecipe([]string{"set", "lemon chicken"}, opts{"dir": dir, "cost-per-serving": "4", "total-cost": "16", "cost-basis": "whole packs"})
	if err != nil {
		t.Fatal(err)
	}
	s, _ := os.ReadFile(p)
	for _, w := range []string{"cost_per_serving: 4.00\n", "total_cost: 16.00\n", "cost_basis: whole packs\n", "- Total $16.00, $4.00 per serving\n", "serves: 4\n"} {
		if !strings.Contains(string(s), w) {
			t.Fatalf("missing %q in\n%s", w, s)
		}
	}
	// adds a key that was missing
	_, err = cmdRecipe([]string{"set", "lemon chicken"}, opts{"dir": dir, "title": "Lemon Chicken"})
	if err != nil {
		t.Fatal(err)
	}
	if got := rec(t, dir, "show", "lemon chicken"); got["title"] != "Lemon Chicken" {
		t.Fatal(got["title"])
	}
}

func TestRecipeSetNoFrontmatterAndBadUsage(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "plain.md"), []byte("# Plain\n\nServes about 2.\n\n## Ingredients\n- rice\n"), 0o644)
	if _, err := cmdRecipe([]string{"set", "plain"}, opts{"dir": dir, "serves": "3"}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "plain.md"))
	if string(b) != "---\nserves: 3\n---\n# Plain\n\nServes about 3.\n\n## Ingredients\n- rice\n" {
		t.Fatalf("%q", b)
	}
	for _, o := range []opts{{}, {"serves": "0"}, {"serves": "x"}, {"cost-per-serving": "-1"}, {"cost-calculated": "yesterday"}} {
		o["dir"] = dir
		_, err := cmdRecipe([]string{"set", "plain"}, o)
		if _, ok := err.(*usageError); !ok {
			t.Fatal(o, err)
		}
	}
}

func TestRecipeMatch(t *testing.T) {
	dir := copyRecipes(t)
	cup := tmpCupboard(t, newHeader+
		"38963,Passata,1,order 1,2026-11-01,700g,700,g,\n"+ // need 2: low
		"346607,Mozzarella,0.57,order 1,2026-11-01,700g,700,g,\n"+ // need 1: low
		"188800,Penne,1,order 1,2026-11-01,500g,500,g,\n"+ // have
		"144329,Onion Brown,3,order 1,2026-11-01,,,,\n") // have
	r, err := cmdRecipe([]string{"match", "pasta bake"}, opts{"dir": dir, "file": cup})
	if err != nil {
		t.Fatal(err)
	}
	if len(r["have"].([]any)) != 2 || len(r["low"].([]any)) != 2 || len(r["missing"].([]any)) != 4 || len(r["noStockcode"].([]any)) != 1 {
		t.Fatal(r)
	}
	low := r["low"].([]any)[0].(obj)
	if low["stockcode"] != "38963" || low["need"] != 2.0 || low["have"] != 1.0 {
		t.Fatal(low)
	}
}

func TestRecipeTitleAndServesFallbacks(t *testing.T) {
	r := parseRecipe("bake", "bake.md", "# Pasta Bake (vegetarian)\n\nServes about 4 people.\n")
	if r.title() != "Pasta Bake" || r.serves() != 4 {
		t.Errorf("title %q, serves %v", r.title(), r.serves())
	}
	r = parseRecipe("plain", "plain.md", "# Plain\n\nNo count here.\n")
	if r.title() != "Plain" || r.serves() != nil {
		t.Errorf("title %q, serves %v", r.title(), r.serves())
	}
	r = parseRecipe("f", "f.md", "---\ntitle: Front Title\nserves: 2.5\n---\n# Heading\n")
	if r.title() != "Front Title" || r.serves() != 2.5 {
		t.Errorf("title %q, serves %v", r.title(), r.serves())
	}
}
