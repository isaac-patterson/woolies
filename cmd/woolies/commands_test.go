package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const searchResults = `{"SearchResultsCount": 3, "Products": [
	{"Products": [{"Stockcode": 11, "DisplayName": "Milk 2L", "Price": 3.1, "IsOnSpecial": true, "WasPrice": 4, "IsAvailable": true}]},
	{"Products": [{"Stockcode": 12, "Name": "Market Milk", "Price": 9, "IsMarketProduct": true, "IsAvailable": true}]},
	{"Stockcode": 13, "DisplayName": "Oat Milk", "Price": 2.5, "IsOnSpecial": false, "IsAvailable": false},
	"junk"
]}`

func TestSearchOneTerm(t *testing.T) {
	f := useFakeSite(t, map[string]any{"POST /apis/ui/Search/products": searchResults})
	res := mustOK(t)(cmdSearch([]string{"milk"}, opts{"special": "true", "sort": "PriceAsc", "page": "2"}))
	if res["totalResults"] != 3.0 {
		t.Errorf("totalResults = %v", res["totalResults"])
	}
	prods := res["products"].([]any)
	if len(prods) != 3 {
		t.Fatalf("got %d products, want 3", len(prods))
	}
	milk, market, oat := prods[0].(obj), prods[1].(obj), prods[2].(obj)
	if milk["wasPrice"] != 4.0 || milk["isOnSpecial"] != true {
		t.Errorf("special product = %v", milk)
	}
	if market["displayName"] != "Market Milk" || market["marketplace"] == nil {
		t.Errorf("market product = %v", market)
	}
	if _, ok := oat["wasPrice"]; ok {
		t.Errorf("wasPrice set on a non-special product: %v", oat)
	}
	b := bodyOf(t, f.calls[0])
	if b["searchTerm"] != "milk" || b["isSpecial"] != true || b["sortType"] != "PriceAsc" || b["pageNumber"] != 2.0 || b["pageSize"] != 8.0 {
		t.Errorf("request body = %v", b)
	}
}

func TestSearchPageSize(t *testing.T) {
	f := useFakeSite(t, map[string]any{"POST /apis/ui/Search/products": searchResults})
	res := mustOK(t)(cmdSearch([]string{"milk"}, opts{"page-size": "50"}))
	if res["warning"] == nil {
		t.Error("want a page-size warning")
	}
	if got := bodyOf(t, f.calls[0])["pageSize"]; got != 36.0 {
		t.Errorf("pageSize sent = %v, want 36", got)
	}
	res = mustOK(t)(cmdSearch([]string{"milk"}, opts{"page-size": "1"}))
	if n := len(res["products"].([]any)); n != 1 {
		t.Errorf("page-size 1 returned %d products", n)
	}
	for _, o := range []opts{{"page-size": "0"}, {"page-size": "x"}, {"page": "x"}} {
		if res := searchOne("milk", o); res["success"] != false || res["error"] == nil {
			t.Errorf("%v: want a failed result, got %v", o, res)
		}
	}
}

func TestSearchManyTerms(t *testing.T) {
	useFakeSite(t, map[string]any{"POST /apis/ui/Search/products": func(body string) (any, error) {
		if strings.Contains(body, `"searchTerm":"bad"`) {
			return nil, errors.New("boom")
		}
		return decode(t, `{"SearchResultsCount": 0, "Products": []}`), nil
	}})
	res, err := cmdSearch([]string{"eggs", "bad"}, opts{})
	if err != nil {
		t.Fatal(err)
	}
	results := res["results"].([]any)
	if res["success"] != false || len(results) != 2 {
		t.Fatalf("result = %v", res)
	}
	if results[0].(obj)["success"] != true || results[0].(obj)["totalResults"] != 0.0 || results[1].(obj)["error"] != "boom" {
		t.Errorf("results = %v", results)
	}
	_, err = cmdSearch(nil, opts{})
	mustUsage(t, err)
}

func TestProductAndCategories(t *testing.T) {
	f := useFakeSite(t, map[string]any{
		"GET /apis/ui/product/detail/a%2Fb":       `{"Product": {"Stockcode": 1}}`,
		"GET /apis/ui/PiesCategoriesWithSpecials": `{"Categories": []}`,
	})
	res := mustOK(t)(cmdProduct([]string{"a/b"}, nil))
	if dig(res, "product", "Product", "Stockcode") != 1.0 {
		t.Errorf("product = %v", res)
	}
	res = mustOK(t)(cmdCategories(nil, nil))
	if dig(res, "categories", "Categories") == nil {
		t.Errorf("categories = %v", res)
	}
	if len(f.calls) != 2 {
		t.Errorf("calls = %v", f.calls)
	}
	_, err := cmdProduct(nil, nil)
	mustUsage(t, err)
	if _, err := cmdProduct([]string{"missing"}, nil); err == nil {
		t.Error("want the request error")
	}
	request = func(string, string, string) (any, error) { return nil, errors.New("down") }
	if _, err := cmdCategories(nil, nil); err == nil {
		t.Error("want the request error")
	}
}

func TestSpecialsByTerm(t *testing.T) {
	f := useFakeSite(t, map[string]any{"POST /apis/ui/Search/products": searchResults})
	res := mustOK(t)(cmdSpecials(nil, opts{"term": "milk"}))
	prods := res["products"].([]any)
	if len(prods) != 1 || prods[0].(obj)["stockcode"] != 11.0 {
		t.Errorf("products = %v", prods)
	}
	if bodyOf(t, f.calls[0])["isSpecial"] != true {
		t.Error("search must ask for specials")
	}
	_, err := cmdSpecials(nil, opts{})
	mustUsage(t, err)
	_, err = cmdSpecials(nil, opts{"term": "x", "page-size": "0"})
	mustUsage(t, err)
	_, err = cmdSpecials(nil, opts{"term": "x", "page": "y"})
	mustUsage(t, err)
}

func TestSpecialsByCategory(t *testing.T) {
	f := useFakeSite(t, map[string]any{"POST /apis/ui/browse/category": `{"TotalRecordCount": 40, "Bundles": [
		{"Products": [{"Stockcode": 1, "DisplayName": "A", "IsOnSpecial": true}]},
		{"Products": [{"Stockcode": 2, "DisplayName": "B", "IsOnSpecial": false}]},
		{"Products": [{"Stockcode": 3, "DisplayName": "C", "IsOnSpecial": true}]}
	]}`})
	res := mustOK(t)(cmdSpecials(nil, opts{"category": "1_ABC", "page-size": "1"}))
	prods := res["products"].([]any)
	if len(prods) != 1 || prods[0].(obj)["stockcode"] != 1.0 || res["totalResults"] != 40.0 {
		t.Errorf("result = %v", res)
	}
	b := bodyOf(t, f.calls[0])
	if b["categoryId"] != "1_ABC" || b["pageSize"] != 36.0 || b["url"] != "/shop/browse/specials" {
		t.Errorf("browse body = %v", b)
	}

	// Without Bundles the Products list is used, and a request error is returned.
	f.routes["POST /apis/ui/browse/category"] = `{"Products": [{"Products": [{"Stockcode": 9, "IsOnSpecial": true}]}]}`
	res = mustOK(t)(cmdSpecials(nil, opts{"category": "x", "category-slug": "fruit"}))
	if len(res["products"].([]any)) != 1 || res["totalResults"] != 0 {
		t.Errorf("result = %v", res)
	}
	f.routes["POST /apis/ui/browse/category"] = errors.New("down")
	if _, err := cmdSpecials(nil, opts{"category": "x"}); err == nil {
		t.Error("want the request error")
	}
	f.routes["POST /apis/ui/Search/products"] = errors.New("down")
	if _, err := cmdSpecials(nil, opts{"term": "x"}); err == nil {
		t.Error("want the request error")
	}
}

const bootstrapJSON = `{"ShopperRequest": {"IsGuest": false, "FulfilmentStoreId": 1},
	"GetDeliveryInfoRequest": {"DeliveryMethod": "Courier", "MinimumSpend": 50,
		"ReservedTime": {"TimeWindow": "9am - 10am"}, "ReservedDate": {"DateText": "Mon 9 Nov"},
		"Address": {"Postcode": "3000"}}}`

const trolleyJSON = `{"FulfilmentStoreId": 1234, "TotalTrolleyItemQuantity": 3, "Totals": {"Total": 9.3},
	"AvailableItems": [{"Stockcode": 11, "Name": "Milk", "Quantity": 2, "Price": 3.1},
		{"Stockcode": 14, "DisplayName": "Bread", "Quantity": 1, "ListPrice": 3.1}]}`

func TestStatus(t *testing.T) {
	resetConfig(t, t.TempDir(), "")
	f := useFakeSite(t, map[string]any{
		"GET /api/ui/v2/bootstrap": bootstrapJSON,
		"GET /apis/ui/Trolley":     trolleyJSON,
	})
	res := mustOK(t)(cmdStatus(nil, nil))
	if res["loggedIn"] != true || res["storeId"] != 1234.0 || res["cartLines"] != 2 || res["cartQuantity"] != 3.0 {
		t.Errorf("status = %v", res)
	}
	if dig(res, "reservedSlot", "window") != "9am - 10am" || dig(res, "reservedSlot", "date") != "Mon 9 Nov" {
		t.Errorf("reservedSlot = %v", res["reservedSlot"])
	}

	// A guest with no delivery info and no cart: store id from the shopper, no slot.
	f.routes["GET /api/ui/v2/bootstrap"] = `{"ShopperRequest": {"IsGuest": true, "FulfilmentStoreId": 7}}`
	f.routes["GET /apis/ui/Trolley"] = errors.New("no cart")
	res = mustOK(t)(cmdStatus(nil, nil))
	if res["loggedIn"] != false || res["storeId"] != 7.0 || res["reservedSlot"] != nil || res["cartLines"] != nil {
		t.Errorf("guest status = %v", res)
	}
	f.routes["GET /api/ui/v2/bootstrap"] = errors.New("down")
	if _, err := cmdStatus(nil, nil); err == nil {
		t.Error("want the bootstrap error")
	}
}

func TestStore(t *testing.T) {
	resetConfig(t, t.TempDir(), "")
	f := useFakeSite(t, map[string]any{
		"GET /api/ui/v2/bootstrap": bootstrapJSON,
		"GET /apis/ui/Trolley":     trolleyJSON,
		"GET /api/v3/ui/fulfilment/stores": `[{"FulfilmentStoreId": 99, "Description": "Other"},
			{"FulfilmentStoreId": 1234, "Description": "Town Centre", "AddressText": "1 Main St", "Suburb": "Town", "Postcode": "3000", "FulfilmentMethod": "Courier"}]`,
	})
	res := mustOK(t)(cmdStore(nil, opts{}))
	if dig(res, "store", "name") != "Town Centre" || res["resolved"] != nil || dig(res, "reservedSlot", "window") == nil {
		t.Errorf("store = %v", res)
	}
	if q := f.callsTo("GET /api/v3/ui/fulfilment/stores")[0].query; q != "postcode=3000" {
		t.Errorf("postcode from the address: query = %q", q)
	}

	// The flag wins; a store that isn't in the list is reported as unresolved.
	mustOK(t)(cmdStore(nil, opts{"postcode": "2000"}))
	if q := f.callsTo("GET /api/v3/ui/fulfilment/stores")[1].query; q != "postcode=2000" {
		t.Errorf("postcode flag: query = %q", q)
	}
	f.routes["GET /apis/ui/Trolley"] = `{"FulfilmentStoreId": 555}`
	res = mustOK(t)(cmdStore(nil, opts{}))
	if res["resolved"] != false || !strings.Contains(str(res["note"]), "555") {
		t.Errorf("unresolved store = %v", res)
	}

	// No delivery method on the account: the store's method fills it in.
	f.routes["GET /apis/ui/Trolley"] = `{"FulfilmentStoreId": 1234}`
	f.routes["GET /api/ui/v2/bootstrap"] = `{"ShopperRequest": {"IsGuest": true}}`
	res = mustOK(t)(cmdStore(nil, opts{"postcode": "3000"}))
	if res["fulfilmentMethod"] != "Courier" {
		t.Errorf("fulfilmentMethod = %v", res["fulfilmentMethod"])
	}

	// No postcode anywhere: no store lookup at all.
	n := len(f.callsTo("GET /api/v3/ui/fulfilment/stores"))
	res = mustOK(t)(cmdStore(nil, opts{}))
	if res["resolved"] != false || !strings.Contains(str(res["note"]), "No postcode") {
		t.Errorf("no postcode = %v", res)
	}
	if len(f.callsTo("GET /api/v3/ui/fulfilment/stores")) != n {
		t.Error("looked up stores without a postcode")
	}

	// The configured postcode is the last resort.
	resetConfig(t, t.TempDir(), `{"postcode": "4000"}`)
	mustOK(t)(cmdStore(nil, opts{}))
	calls := f.callsTo("GET /api/v3/ui/fulfilment/stores")
	if q := calls[len(calls)-1].query; q != "postcode=4000" {
		t.Errorf("configured postcode: query = %q", q)
	}
	f.routes["GET /api/ui/v2/bootstrap"] = errors.New("down")
	if _, err := cmdStore(nil, opts{}); err == nil {
		t.Error("want the bootstrap error")
	}
}

func TestCartAndTrimCart(t *testing.T) {
	long := strings.Repeat("x", 400)
	f := useFakeSite(t, map[string]any{"GET /apis/ui/Trolley": `{
		"AvailableItems": [{"Stockcode": 1, "Name": "Milk", "Quantity": 2, "Price": 3, "SalePrice": 2.5, "WasPrice": 3.5}],
		"BundleItems": [{"Bundle": {"Items": [{"Stockcode": 2, "Description": "Bundle part", "Quantity": 1}]}}],
		"UnavailableItems": [{"Stockcode": 3, "DisplayName": "Gone"}],
		"Errors": ["plain", {"Message": "with message"}, {"Code": "` + long + `"}, 5],
		"Totals": {"Total": 6}, "TotalTrolleyItemQuantity": 3, "DeliveryFee": {"Total": 9}, "FulfilmentStoreId": 77}`})
	res := mustOK(t)(cmdCart(nil, nil))
	cart := res["cart"].(obj)
	items := cart["items"].([]any)
	if len(items) != 3 {
		t.Fatalf("items = %v", items)
	}
	milk, part, gone := items[0].(obj), items[1].(obj), items[2].(obj)
	if milk["salePrice"] != 2.5 || milk["wasPrice"] != 3.5 || milk["status"] != nil {
		t.Errorf("milk = %v", milk)
	}
	if part["name"] != "Bundle part" || part["status"] != "BundleItems" {
		t.Errorf("bundle part = %v", part)
	}
	if gone["status"] != "UnavailableItems" || gone["qty"] != nil {
		t.Errorf("unavailable = %v", gone)
	}
	errs := cart["errors"].([]any)
	if len(errs) != 3 || errs[0] != "plain" || errs[1] != "with message" || len(errs[2].(string)) != 300 {
		t.Errorf("errors = %v", errs)
	}
	if cart["deliveryFee"] != 9.0 || cart["FulfilmentStoreId"] != 77.0 || cart["totalQuantity"] != 3.0 {
		t.Errorf("cart = %v", cart)
	}
	f.routes["GET /apis/ui/Trolley"] = errors.New("down")
	if _, err := cmdCart(nil, nil); err == nil {
		t.Error("want the request error")
	}
	if empty := trimCart(nil); len(empty["items"].([]any)) != 0 || empty["totals"] != nil {
		t.Errorf("trimCart(nil) = %v", empty)
	}
}

// updateItems returns the {stockcode: quantity} pairs sent in the last trolley update.
func updateItems(t *testing.T, f *fakeSite) map[float64]float64 {
	t.Helper()
	calls := f.callsTo("POST /api/v3/ui/trolley/update")
	if len(calls) == 0 {
		t.Fatal("no trolley update was sent")
	}
	out := map[float64]float64{}
	for _, it := range asArr(bodyOf(t, calls[len(calls)-1])["items"]) {
		m := asObj(it)
		out[m["stockcode"].(float64)] = m["quantity"].(float64)
	}
	return out
}

func cartSite(t *testing.T) *fakeSite {
	return useFakeSite(t, map[string]any{
		"POST /api/v3/ui/trolley/update": `{"AvailableItems": []}`,
		"GET /apis/ui/Trolley":           trolleyJSON,
	})
}

func TestCartWrites(t *testing.T) {
	f := cartSite(t)
	res := mustOK(t)(cmdAdd([]string{"11"}, nil))
	if got := updateItems(t, f); got[11] != 1 || len(got) != 1 {
		t.Errorf("add sent %v", got)
	}
	if len(dig(res, "cart", "items").([]any)) != 2 {
		t.Errorf("add should return the cart read back: %v", res)
	}
	mustOK(t)(cmdAdd([]string{"11", "4"}, nil))
	if got := updateItems(t, f); got[11] != 4 {
		t.Errorf("add 4 sent %v", got)
	}
	mustOK(t)(cmdSetQty([]string{"11", "0"}, nil))
	if got := updateItems(t, f); got[11] != 0 {
		t.Errorf("set-qty sent %v", got)
	}
	mustOK(t)(cmdRemove([]string{"14"}, nil))
	if got := updateItems(t, f); got[14] != 0 {
		t.Errorf("remove sent %v", got)
	}

	for _, c := range []struct {
		fn  func([]string, opts) (obj, error)
		pos []string
	}{
		{cmdAdd, nil}, {cmdAdd, []string{"x"}}, {cmdAdd, []string{"-3"}}, {cmdAdd, []string{"1", "0"}}, {cmdAdd, []string{"1", "x"}},
		{cmdSetQty, []string{"1"}}, {cmdSetQty, []string{"x", "1"}}, {cmdSetQty, []string{"1", "-1"}},
		{cmdRemove, nil}, {cmdRemove, []string{"0"}},
	} {
		_, err := c.fn(c.pos, nil)
		mustUsage(t, err)
	}

	// The update worked but the cart can't be read back: still a success, with a note.
	f.routes["GET /apis/ui/Trolley"] = errors.New("down")
	res = mustOK(t)(cmdAdd([]string{"11"}, nil))
	if !strings.Contains(str(res["note"]), "could not read the cart back") {
		t.Errorf("note = %v", res["note"])
	}
	f.routes["POST /api/v3/ui/trolley/update"] = errors.New("rejected")
	if _, err := cmdAdd([]string{"11"}, nil); err == nil {
		t.Error("want the update error")
	}
}

func TestAddMany(t *testing.T) {
	f := cartSite(t)
	dir := t.TempDir()
	file := func(body string) string {
		p := filepath.Join(dir, "items.json")
		os.WriteFile(p, []byte(body), 0o600)
		return p
	}
	mustOK(t)(cmdAddMany([]string{file(`[{"stockcode": 11, "quantity": 3}, {"stockcode": "14"}]`)}, nil))
	if got := updateItems(t, f); got[11] != 3 || got[14] != 1 {
		t.Errorf("add-many sent %v", got)
	}

	// "-" reads stdin.
	r, w, _ := os.Pipe()
	w.WriteString(`[{"stockcode": 20, "quantity": 2}]`)
	w.Close()
	old := os.Stdin
	os.Stdin = r
	t.Cleanup(func() { os.Stdin = old })
	mustOK(t)(cmdAddMany([]string{"-"}, nil))
	if got := updateItems(t, f); got[20] != 2 {
		t.Errorf("add-many from stdin sent %v", got)
	}

	for _, pos := range [][]string{
		nil,
		{filepath.Join(dir, "missing.json")},
		{file(`{"not": "an array"}`)},
		{file(`[]`)},
		{file(`[{"stockcode": "abc"}]`)},
		{file(`[{"stockcode": 1, "quantity": -1}]`)},
		{file(`[{"stockcode": 1, "quantity": 0}]`)},
	} {
		_, err := cmdAddMany(pos, nil)
		mustUsage(t, err)
	}
}

func TestClear(t *testing.T) {
	f := useFakeSite(t, map[string]any{
		"GET /apis/ui/Trolley":           `{"AvailableItems": [{"Stockcode": 1}, {"Stockcode": 1}, {"Stockcode": 2}, {"Stockcode": "bad"}]}`,
		"POST /api/v3/ui/trolley/update": `{}`,
	})
	res := mustOK(t)(cmdClear(nil, opts{"dry-run": "true"}))
	if res["wouldRemove"] != 2 || len(f.callsTo("POST /api/v3/ui/trolley/update")) != 0 {
		t.Errorf("dry run = %v, calls %v", res, f.calls)
	}
	res = mustOK(t)(cmdClear(nil, opts{}))
	if got := updateItems(t, f); res["removed"] != 2 || len(got) != 2 || got[1] != 0 || got[2] != 0 {
		t.Errorf("clear = %v, sent %v", res, got)
	}

	f.routes["GET /apis/ui/Trolley"] = `{}`
	res = mustOK(t)(cmdClear(nil, opts{}))
	if res["removed"] != 0 || len(f.callsTo("POST /api/v3/ui/trolley/update")) != 1 {
		t.Errorf("empty cart = %v", res)
	}
	f.routes["GET /apis/ui/Trolley"] = `{"AvailableItems": [{"Stockcode": 5}]}`
	f.routes["POST /api/v3/ui/trolley/update"] = errors.New("rejected")
	if _, err := cmdClear(nil, opts{}); err == nil {
		t.Error("want the update error")
	}
	f.routes["GET /apis/ui/Trolley"] = errors.New("down")
	if _, err := cmdClear(nil, opts{}); err == nil {
		t.Error("want the read error")
	}
}

func TestQuickAdd(t *testing.T) {
	f := useFakeSite(t, map[string]any{
		"POST /apis/ui/Search/products": `{"Products": [
			{"Stockcode": 1, "DisplayName": "Unavailable", "IsAvailable": false},
			{"Stockcode": 2, "DisplayName": "Marketplace", "IsAvailable": true, "IsMarketProduct": true},
			{"Stockcode": 3, "DisplayName": "Good", "IsAvailable": true}]}`,
		"POST /api/v3/ui/trolley/update": `{}`,
		"GET /apis/ui/Trolley":           trolleyJSON,
	})
	res := mustOK(t)(cmdQuickAdd([]string{"free", "range", "eggs", "2"}, opts{}))
	if dig(res, "picked", "stockcode") != 3.0 || res["quantity"] != 2 {
		t.Errorf("quick-add = %v", res)
	}
	if got := updateItems(t, f); got[3] != 2 {
		t.Errorf("sent %v", got)
	}
	if b := bodyOf(t, f.callsTo("POST /apis/ui/Search/products")[0]); b["searchTerm"] != "free range eggs" {
		t.Errorf("search body = %v", b)
	}

	n := len(f.callsTo("POST /api/v3/ui/trolley/update"))
	res = mustOK(t)(cmdQuickAdd([]string{"eggs"}, opts{"dry-run": "true"}))
	if res["dryRun"] != true || res["quantity"] != 1 || len(f.callsTo("POST /api/v3/ui/trolley/update")) != n {
		t.Errorf("dry run = %v", res)
	}

	_, err := cmdQuickAdd(nil, opts{})
	mustUsage(t, err)
	_, err = cmdQuickAdd([]string{"eggs", "0"}, opts{})
	mustUsage(t, err)

	f.routes["POST /api/v3/ui/trolley/update"] = errors.New("rejected")
	if _, err := cmdQuickAdd([]string{"eggs"}, opts{}); err == nil {
		t.Error("want the update error")
	}
	f.routes["POST /apis/ui/Search/products"] = `{"Products": [{"Stockcode": 2, "IsMarketProduct": true}]}`
	if res, _ := cmdQuickAdd([]string{"eggs"}, opts{}); res["success"] != false {
		t.Errorf("nothing suitable = %v", res)
	}
	f.routes["POST /apis/ui/Search/products"] = `{"Products": [{"Stockcode": "abc"}]}`
	_, err = cmdQuickAdd([]string{"eggs"}, opts{})
	mustUsage(t, err)
	f.routes["POST /apis/ui/Search/products"] = errors.New("down")
	if res, _ := cmdQuickAdd([]string{"eggs"}, opts{}); res["success"] != false || res["error"] != "down" {
		t.Errorf("search failure = %v", res)
	}
}

const addressesJSON = `{"Address": [
	{"AddressId": 1, "AddressText": "1 First St", "SuburbName": "A", "PostalCode": "3000", "AreaId": 10, "IsPrimary": false},
	{"AddressId": 2, "AddressText": "2 Second St", "SuburbName": "B", "PostalCode": "3001", "AreaId": 20, "IsPrimary": true},
	"junk"]}`

func TestAddresses(t *testing.T) {
	f := useFakeSite(t, map[string]any{"GET /apis/ui/Address": addressesJSON})
	res := mustOK(t)(cmdAddresses(nil, nil))
	list := res["addresses"].([]any)
	if res["count"] != 2 || list[1].(obj)["isPrimary"] != true || list[0].(obj)["postcode"] != "3000" {
		t.Errorf("addresses = %v", res)
	}
	f.routes["GET /apis/ui/Address"] = errors.New("down")
	if _, err := cmdAddresses(nil, nil); err == nil {
		t.Error("want the request error")
	}
}

func TestTimeslots(t *testing.T) {
	f := useFakeSite(t, map[string]any{
		"GET /apis/ui/Address": addressesJSON,
		"GET /api/v3/ui/fulfilment/windows": `{"Days": [
			{"Date": "2026-11-10", "Available": true, "Times": [
				{"Id": 1, "TimeWindow": "7-8", "Available": true, "Price": 5},
				{"Id": 2, "TimeWindow": "8-9", "Available": false},
				"junk"]},
			{"Date": "2026-11-11", "Available": false, "ClosedText": "Closed", "Times": []},
			"junk"]}`,
	})
	res := mustOK(t)(cmdTimeslots(nil, opts{}))
	days := res["days"].([]any)
	if res["slotCount"] != 1 || len(days) != 1 || res["addressId"] != 2.0 || res["method"] != "Courier" || res["note"] != nil {
		t.Errorf("timeslots = %v", res)
	}
	q := f.callsTo("GET /api/v3/ui/fulfilment/windows")[0].query
	if !strings.Contains(q, "addressId=2") || !strings.Contains(q, "areaId=20") || !strings.Contains(q, "fulfilmentMethod=Courier") {
		t.Errorf("query = %q", q)
	}

	res = mustOK(t)(cmdTimeslots(nil, opts{"all": "true", "address": "1", "method": "pICKUP"}))
	if res["slotCount"] != 2 || len(res["days"].([]any)) != 2 || res["method"] != "Pickup" || res["addressId"] != 1.0 {
		t.Errorf("--all = %v", res)
	}

	_, err := cmdTimeslots(nil, opts{"method": "Drone"})
	mustUsage(t, err)
	_, err = cmdTimeslots(nil, opts{"address": "999"})
	mustUsage(t, err)

	f.routes["GET /api/v3/ui/fulfilment/windows"] = `{"Days": []}`
	if res := mustOK(t)(cmdTimeslots(nil, opts{})); res["note"] == nil {
		t.Error("want a note when no days come back")
	}
	f.routes["GET /api/v3/ui/fulfilment/windows"] = errors.New("down")
	if _, err := cmdTimeslots(nil, opts{}); err == nil {
		t.Error("want the windows error")
	}
	// No primary address: the first one is used.
	f.routes["GET /apis/ui/Address"] = `{"Address": [{"AddressId": 5, "AreaId": 1}]}`
	f.routes["GET /api/v3/ui/fulfilment/windows"] = `{"Days": []}`
	if res := mustOK(t)(cmdTimeslots(nil, opts{})); res["addressId"] != 5.0 {
		t.Errorf("fallback address = %v", res["addressId"])
	}
	f.routes["GET /apis/ui/Address"] = `{"Address": []}`
	if _, err := cmdTimeslots(nil, opts{}); err == nil {
		t.Error("want an error with no saved addresses")
	}
	f.routes["GET /apis/ui/Address"] = errors.New("down")
	if _, err := cmdTimeslots(nil, opts{}); err == nil {
		t.Error("want the address error")
	}
}
