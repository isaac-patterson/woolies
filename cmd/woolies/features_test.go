package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const featureTrolley = `{"FulfilmentStoreId": 1, "TotalTrolleyItemQuantity": 4, "Totals": {"Total": 122},
	"AvailableItems": [
	 {"Stockcode": 11, "Name": "Milk 2L", "Quantity": 2, "Price": 3.1, "PackageSize": "2L", "CupString": "$1.55 / 1L"},
	 {"Stockcode": 12, "Name": "Beef Scotch Fillet Steak Medium 280g - 800g", "Quantity": 1, "Price": 34.4, "WasPrice": 40, "SalePrice": 34.4},
	 {"Stockcode": 170225, "Name": "Zucchini", "Quantity": 1, "Price": 5}],
	"UnavailableItems": [{"Stockcode": 13, "Name": "Gone", "Quantity": 1, "Price": 1}]}`

const featureOffers = `{"data":[
 {"id":"mult","attributes":{"boostState":"BOOSTED","summary":{"bannerIds":["1005"]},"display":{"heading":"Collect 10x points"},
  "coupons":[{"attributes":{"reward":{"type":"POINTS_MULTIPLIER","value":10},"redemption":{"minimumSpend":10000,"eligibleSalesChannels":["ONLINE_WEB"]}}}]}},
 {"id":"fixed","attributes":{"boostState":"NOT_BOOSTED","summary":{"bannerIds":["1005"]},"display":{"heading":"Collect 1800 points"},
  "coupons":[{"attributes":{"reward":{"type":"POINTS_FIXED","value":1800},"redemption":{"minimumSpend":12000,"eligibleSalesChannels":["ONLINE_WEB"]}}}]}},
 {"id":"zuc","attributes":{"boostState":"BOOSTED","summary":{"bannerIds":["1005"]},"display":{"heading":"Zucchini booster","primaryProductId":"170225"},
  "coupons":[{"attributes":{"reward":{"type":"POINTS_FIXED","value":60},"redemption":{"minimumSpend":300,"eligibleSalesChannels":["ONLINE_WEB"]}}}]}},
 {"id":"shop","attributes":{"boostState":"BOOSTED","summary":{"bannerIds":["1005"]},"display":{"heading":"In store only"},
  "coupons":[{"attributes":{"reward":{"type":"POINTS_FIXED","value":5},"redemption":{"minimumSpend":0,"eligibleSalesChannels":["INSTORE"]}}}]}}
]}`

func TestVariableWeightAndCompactCart(t *testing.T) {
	c := trimCart(decode(t, featureTrolley))
	items := c["items"].([]any)
	steak := items[1].(obj)
	if steak["isVariableWeight"] != true || steak["priceIsEstimate"] != true || steak["isOnSpecial"] != true {
		t.Fatalf("steak = %v", steak)
	}
	if items[0].(obj)["isVariableWeight"] != nil {
		t.Fatal("milk is not variable weight")
	}
	out := obj{"cart": c}
	compactResult(out)
	lines := asObj(out["cart"])["lines"].([]any)
	if len(lines) != 4 {
		t.Fatalf("lines = %v", lines)
	}
	if got := lines[0].(string); got != "11 | Milk 2L | 2L | $3.10 | $1.55 / 1L |  | x2" {
		t.Errorf("milk line = %q", got)
	}
	if got := lines[1].(string); !strings.Contains(got, "~$34.40") || !strings.Contains(got, "SPECIAL (was $40.00)") || !strings.Contains(got, "VARIABLE-WEIGHT") {
		t.Errorf("steak line = %q", got)
	}
	if got := lines[3].(string); !strings.Contains(got, "UNAVAILABLE:UnavailableItems") {
		t.Errorf("unavailable line = %q", got)
	}
}

func TestCompactSearchAndTrimProduct(t *testing.T) {
	p := trimProduct(obj{"Stockcode": 5.0, "DisplayName": "Steak 280g - 800g", "Price": 34.4, "IsAvailable": true})
	if p["isVariableWeight"] != true || p["priceIsEstimate"] != true {
		t.Fatalf("p = %v", p)
	}
	if trimProduct(obj{"Stockcode": 6.0, "DisplayName": "Milk", "Price": 1.0})["isVariableWeight"] != nil {
		t.Fatal("plain product flagged")
	}
	out := obj{"results": []any{obj{"products": []any{p}}}}
	compactResult(out)
	lines := asObj(out["results"].([]any)[0])["lines"].([]any)
	if len(lines) != 1 || !strings.HasPrefix(lines[0].(string), "5 | Steak 280g - 800g |") {
		t.Fatalf("lines = %v", lines)
	}
}

func TestCartSummaryAndOffersCart(t *testing.T) {
	resetConfig(t, t.TempDir(), "")
	useFakeSession(t)
	useFakeOffers(t, []capturedBody{{"x", decode(t, featureOffers)}}, nil)
	useFakeSite(t, map[string]any{
		"GET /apis/ui/Trolley":     featureTrolley,
		"GET /api/ui/v2/bootstrap": bootstrapJSON,
	})
	res := mustOK(t)(cmdCart(nil, opts{"summary": "true"}))
	sum := asObj(res["summary"])
	if sum["total"] != 122.0 || sum["itemCount"] != 4.0 || sum["minimumSpend"] != 50.0 || sum["shortfallToMin"] != 0.0 {
		t.Fatalf("summary = %v", sum)
	}
	if sum["totalSavings"] != 5.6 {
		t.Errorf("savings = %v", sum["totalSavings"])
	}
	short := sum["boostedOfferShortfalls"].([]any)
	if len(short) != 1 { // only the boosted 10x offer: the 1800 one is not boosted, the zucchini one is per product
		t.Fatalf("shortfalls = %v", short)
	}

	res = mustOK(t)(cmdOffers(nil, opts{"cart": "true"}))
	chk := asObj(res["cartCheck"])
	// 10x on $122 = 1220 (base included) + zucchini 60; the 1800 offer qualifies but is not boosted.
	if chk["estimatedPoints"] != 1280.0 {
		t.Fatalf("points = %v, %v", chk["estimatedPoints"], chk)
	}
	if len(chk["notOnline"].([]any)) != 1 || len(chk["warnings"].([]any)) != 1 {
		t.Fatalf("check = %v", chk)
	}
}

func TestOfferEvalThresholdCrossing(t *testing.T) {
	offers := offerList([]capturedBody{{"x", decode(t, strings.Replace(featureOffers, `"NOT_BOOSTED"`, `"BOOSTED"`, 1))}})
	cart := func(total float64) obj {
		return obj{"totals": obj{"Total": total}, "items": []any{}}
	}
	hi := offerEval(cart(122), offers, 30)
	if hi["estimatedPoints"] != 1220.0+1800 {
		t.Fatalf("at $122: %v", hi["estimatedPoints"])
	}
	lo := offerEval(cart(116.31), offers, 30)
	if lo["estimatedPoints"] != 1163.0 {
		t.Fatalf("at $116.31 the 1800 offer should drop out: %v", lo["estimatedPoints"])
	}
	if w := lo["warnings"].([]any); len(w) == 0 || !strings.Contains(w[0].(string), "$3.69") {
		t.Fatalf("warnings = %v", w)
	}
	if len(offerEval(cart(5), offers, 30)["farAway"].([]any)) == 0 {
		t.Fatal("expected far away offers")
	}
}

func TestOrdersCancelledAndActive(t *testing.T) {
	win := obj{"start": "10:00", "end": "11:00"}
	orders := []any{
		trimOrderSummary(obj{"OrderId": 100.0, "CurrentStatus": "Cancelled", "CreatedDate": "2026-11-10T09:00:00", "DeliveryMethod": "Pickup", "ProjectedDeliveryTime": obj{"StartTime": "10:00", "EndTime": "11:00"}}),
		trimOrderSummary(obj{"OrderId": 101.0, "CurrentStatus": "Received", "CreatedDate": "2026-11-10T09:10:00", "DeliveryMethod": "Pickup", "ProjectedDeliveryTime": obj{"StartTime": "10:00", "EndTime": "11:00"}}),
	}
	_ = win
	markReplaced(orders)
	c := orders[0].(obj)
	if c["cancelled"] != true || c["replacedBy"] != 101.0 || c["note"] == nil {
		t.Fatalf("cancelled = %v", c)
	}
	if orders[1].(obj)["cancelled"] != nil {
		t.Fatal("active order flagged")
	}
	act := activeOrders(orders)
	if len(act) != 1 || asObj(act[0])["orderId"] != 101.0 {
		t.Fatalf("active = %v", act)
	}
}

func TestOrdersActiveFlagAndTopLevelWindow(t *testing.T) {
	useFakeSession(t)
	list := `[{"url":"x/orders","json":[
	 {"OrderId": 100, "CurrentStatus": "Cancelled", "CreatedDate": "2026-11-10T09:10:00", "DeliveryMethod": "Pickup", "ProjectedDeliveryTime": {"StartTime": "10:00", "EndTime": "11:00"}},
	 {"OrderId": 99, "CurrentStatus": "Collected", "CreatedDate": "2026-11-01T09:00:00", "DeliveryMethod": "Pickup", "ProjectedDeliveryTime": {"StartTime": "08:00", "EndTime": "09:00"}}]}]`
	useFakeOrders(t, map[string]string{"/shop/myaccount/myorders": list})
	res := mustOK(t)(cmdOrders(nil, opts{}))
	if res["count"] != 2 || asObj(res["orders"].([]any)[0])["cancelled"] != true {
		t.Fatalf("all = %v", res)
	}
	res = mustOK(t)(cmdOrders(nil, opts{"active": "true"}))
	if res["count"] != 1 || dig(res, "window", "start") != "08:00" {
		t.Fatalf("active = %v", res)
	}
	if _, err := cmdOrders(nil, opts{"active": "true", "order-id": "5"}); err == nil {
		t.Fatal("want usage error")
	}
}

func TestSpecialsMatchNeedsAll(t *testing.T) {
	_, err := cmdSpecials(nil, opts{"match": "x", "term": "milk"})
	mustUsage(t, err)
	_, err = cmdSpecials(nil, opts{"all": "true", "match": "("})
	mustUsage(t, err)
}

func writeCupboardFile(t *testing.T, rows string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "c.csv")
	if err := os.WriteFile(p, []byte(newHeader+rows), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRecipeCart(t *testing.T) {
	dir := copyRecipes(t)
	cup := writeCupboardFile(t,
		"38963,Passata,0.35,order 1,2026-11-01,700g,700,g,\n"+ // need 2, has 0.35: buy 2
			"188800,Penne,1,order 1,2026-11-01,500g,500,g,\n"+ // covered
			"144329,Onion Brown,0.35,order 1,2026-11-01,,,,\n") // need 2, buy 2
	res, err := cmdRecipe([]string{"cart", "pasta bake"}, opts{"dir": dir, "file": cup})
	if err != nil {
		t.Fatal(err)
	}
	add := res["addMany"].([]any)
	got := map[int]int{}
	for _, a := range add {
		m := a.(obj)
		got[m["stockcode"].(int)] = m["quantity"].(int)
	}
	if got[38963] != 2 || got[144329] != 2 || got[346607] != 1 || got[188800] != 0 || len(add) != 7 {
		t.Fatalf("addMany = %v", add)
	}
	skipped := res["skipped"].([]any)
	if len(skipped) != 2 { // the covered penne and the no-stockcode line
		t.Fatalf("skipped = %v", skipped)
	}
}

func TestRecipeSaveCostsAndRoundTrip(t *testing.T) {
	dir := t.TempDir()
	in := `{"title":"Test Bake","serves":3,"ingredients":["200g pasta","1 tin tomatoes"],"method":["Boil.","Bake."],
	 "notes":["Keeps 3 days."],
	 "shopping":[{"name":"Pasta 500g","stockcode":111111,"price":1.0,"share":0.4,"detail":"200g of 500g"},
	  {"name":"Tomatoes","stockcode":222222,"quantity":2,"price":1.25},
	  {"name":"Cheese","stockcode":333333,"price":3.33,"share":0.5}],
	 "atHome":["Salt"],"date":"2026-11-10"}`
	f := filepath.Join(t.TempDir(), "in.json")
	os.WriteFile(f, []byte(in), 0o644)
	res, err := cmdRecipe([]string{"save", f}, opts{"dir": dir})
	if err != nil {
		t.Fatal(err)
	}
	// 0.40 + 2.50 + 1.67 (1.665 rounds up) = 4.57; 4.57 / 3 = 1.52
	if res["totalCost"] != 4.57 || res["costPerServing"] != 1.52 || res["slug"] != "test-bake" {
		t.Fatalf("res = %v", res)
	}
	shown := rec(t, dir, "show", "test-bake")
	fm := asObj(shown["frontmatter"])
	if fm["total_cost"] != 4.57 || fm["cost_per_serving"] != 1.52 || shown["serves"] != 3 {
		t.Fatalf("frontmatter = %v", fm)
	}
	cost := asObj(shown["costBreakdown"])
	if cost["total"] != 4.57 || len(cost["items"].([]any)) != 3 || len(shown["shoppingList"].([]any)) != 4 {
		t.Fatalf("cost = %v", cost)
	}
	// Refuses to overwrite without --force, then keeps a .bak with it.
	_, err = cmdRecipe([]string{"save", f}, opts{"dir": dir})
	mustUsage(t, err)
	if _, err := cmdRecipe([]string{"save", f}, opts{"dir": dir, "force": "true"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "test-bake.md.bak")); err != nil {
		t.Fatal(err)
	}
}

func TestRecipeSaveValidation(t *testing.T) {
	dir := t.TempDir()
	for name, in := range map[string]string{
		"no title":    `{"serves":2,"ingredients":["a"],"method":["b"]}`,
		"no serves":   `{"title":"T","ingredients":["a"],"method":["b"]}`,
		"no method":   `{"title":"T","serves":2,"ingredients":["a"]}`,
		"bad share":   `{"title":"T","serves":2,"ingredients":["a"],"method":["b"],"shopping":[{"name":"x","stockcode":123456,"price":1,"share":2}]}`,
		"no price":    `{"title":"T","serves":2,"ingredients":["a"],"method":["b"],"shopping":[{"name":"x","stockcode":123456}]}`,
		"unknown key": `{"title":"T","serves":2,"ingredients":["a"],"method":["b"],"colour":"red"}`,
		"bad slug":    `{"title":"T","slug":"Bad Slug","serves":2,"ingredients":["a"],"method":["b"]}`,
	} {
		f := filepath.Join(t.TempDir(), "in.json")
		os.WriteFile(f, []byte(in), 0o644)
		_, err := cmdRecipe([]string{"save", f}, opts{"dir": dir})
		if err == nil {
			t.Errorf("%s: want an error", name)
			continue
		}
		mustUsage(t, err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Errorf("invalid input wrote files: %v", ents)
	}
	// --dry-run writes nothing and returns the markdown.
	f := filepath.Join(t.TempDir(), "in.json")
	os.WriteFile(f, []byte(`{"title":"Dry Run","serves":2,"ingredients":["a"],"method":["b"]}`), 0o644)
	res, err := cmdRecipe([]string{"save", f}, opts{"dir": dir, "dry-run": "true"})
	if err != nil || !strings.Contains(res["markdown"].(string), "# Dry Run") {
		t.Fatalf("dry run: %v %v", res, err)
	}
	if ents, _ := os.ReadDir(dir); len(ents) != 0 {
		t.Error("dry run wrote files")
	}
}

func TestOfferEvalEmptyCartEarnsNothing(t *testing.T) {
	offers := offerList([]capturedBody{{"x", decode(t, strings.ReplaceAll(featureOffers, `"minimumSpend":300`, `"minimumSpend":0`))}})
	res := offerEval(obj{"totals": obj{"Total": 0.0}, "items": []any{}}, offers, 30)
	if res["estimatedPoints"] != 0.0 || len(res["qualifies"].([]any)) != 0 {
		t.Fatalf("empty cart: %v", res)
	}
}

func TestCompactSetting(t *testing.T) {
	resetConfig(t, t.TempDir(), "")
	if on, err := compactWanted(opts{}); on || err != nil {
		t.Fatalf("default: %v %v", on, err)
	}
	if on, _ := compactWanted(opts{"compact": "true"}); !on {
		t.Fatal("--compact")
	}
	t.Setenv("WOOLIES_COMPACT", "true")
	if on, _ := compactWanted(opts{}); !on {
		t.Fatal("env setting")
	}
	if on, _ := compactWanted(opts{"no-compact": "true"}); on {
		t.Fatal("--no-compact should win over the setting")
	}
	if on, _ := compactWanted(opts{"nutrition": "true"}); on {
		t.Fatal("--nutrition should win over the setting")
	}
	_, err := compactWanted(opts{"compact": "true", "nutrition": "true"})
	mustUsage(t, err)
	_, err = compactWanted(opts{"compact": "true", "no-compact": "true"})
	mustUsage(t, err)
	t.Setenv("WOOLIES_COMPACT", "maybe")
	_, err = compactWanted(opts{})
	mustUsage(t, err)
}

func TestCompactFromConfigFile(t *testing.T) {
	resetConfig(t, t.TempDir(), `{"compact": true}`)
	if on, err := compactWanted(opts{}); !on || err != nil {
		t.Fatalf("config: %v %v", on, err)
	}
}
