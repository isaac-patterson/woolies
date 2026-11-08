package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Orders, cupboard add-latest and specials --all, run against the fake site and fake order pages.

const ordersListPage = `[{"url": "https://x/orders/api/orders", "json": {"items": [
	{"OrderId": 100, "CreatedDate": "2026-10-01T10:00:00", "CurrentStatus": "Collected", "Total": 50,
	 "DeliveryMethod": "Pickup", "PickupStoreNumber": 7, "ProjectedDeliveryTime": {"StartTime": "9:00", "EndTime": "10:00"}},
	{"OrderId": 90, "CreatedDate": "2026-11-02T09:00:00", "Status": "Prepared", "Total": 80, "DeliveryWindowStart": "11:00"},
	{"OrderId": 100, "CreatedDate": "2026-10-01T10:00:00"}]}}]`

const orderDetailPage = `[
	{"url": "https://x/orders/api/orders/90", "json": {"OrderId": 90, "Total": 81, "Items": [
		{"Product": {"Stockcode": 1, "DisplayName": "Milk 2L", "Brand": "Dairy"}, "Quantity": 2, "Total": 6.2},
		{"Product": {"Stockcode": 1, "DisplayName": "Milk 2L"}, "Quantity": 2, "Total": 6.2},
		{"Product": {"Stockcode": 2, "Name": "Bread"}, "Qty": 1, "UnitPrice": 4}]}},
	{"url": "https://x/orders/api/other", "json": {"Unrelated": [{"Stockcode": 5}]}}]`

func orderPages() map[string]string {
	return map[string]string{
		"/shop/myaccount/myorders":    ordersListPage,
		"/shop/myaccount/myorders/90": orderDetailPage,
	}
}

func TestOrdersList(t *testing.T) {
	useFakeSession(t)
	useFakeOrders(t, orderPages())
	res := mustOK(t)(cmdOrders(nil, opts{}))
	orders := res["orders"].([]any)
	if res["count"] != 2 || len(orders) != 2 {
		t.Fatalf("orders = %v", res)
	}
	first := orders[0].(obj)
	if first["orderId"] != 100.0 || first["status"] != "Collected" || first["method"] != "Pickup" || first["pickupStore"] != 7.0 ||
		dig(first, "window", "start") != "9:00" {
		t.Errorf("first order = %v", first)
	}
	if second := orders[1].(obj); second["status"] != "Prepared" || second["window"] != "11:00" {
		t.Errorf("second order = %v", second)
	}
	res = mustOK(t)(cmdOrders(nil, opts{"limit": "1"}))
	if res["count"] != 2 || len(res["orders"].([]any)) != 1 {
		t.Errorf("limit 1 = %v", res)
	}
	mustOK(t)(cmdOrders(nil, opts{"limit": "0"}))
	mustOK(t)(cmdOrders(nil, opts{"limit": "99"}))

	for _, o := range []opts{{"latest": "true", "order-id": "1"}, {"order-id": "abc"}, {"limit": "x"}} {
		_, err := cmdOrders(nil, o)
		mustUsage(t, err)
	}
}

func TestOrderDetail(t *testing.T) {
	useFakeSession(t)
	useFakeOrders(t, orderPages())
	res := mustOK(t)(cmdOrders(nil, opts{"order-id": "90"}))
	items := res["items"].([]any)
	if res["itemCount"] != 2 || res["total"] != 81.0 || res["orderId"] != 90.0 {
		t.Fatalf("detail = %v", res)
	}
	milk, bread := items[0].(obj), items[1].(obj)
	if milk["stockcode"] != 1.0 || milk["name"] != "Milk 2L" || milk["brand"] != "Dairy" || milk["qty"] != 2.0 || milk["lineTotal"] != 6.2 {
		t.Errorf("milk = %v", milk)
	}
	if bread["qty"] != 1.0 || bread["unitPrice"] != 4.0 || bread["name"] != "Bread" {
		t.Errorf("bread = %v", bread)
	}

	// An order whose page has no item lists: not a success, with the JSON layout for debugging.
	pages := orderPages()
	pages["/shop/myaccount/myorders/55"] = `[{"url": "https://x/orders/api/orders/55", "json": [{"OrderId": 55, "Flag": true, "Note": null, "List": []}]}]`
	useFakeOrders(t, pages)
	res, err := cmdOrders(nil, opts{"order-id": "55"})
	if err != nil || res["success"] != false || res["shape"] == nil || res["note"] == nil {
		t.Errorf("no items = %v, %v", res, err)
	}
	if shape := dig(res["shape"].([]any)[0].([]any)[0], "Flag"); shape != "boolean" {
		t.Errorf("shape = %v", res["shape"])
	}

	useFakeOrders(t, map[string]string{})
	if _, err := cmdOrders(nil, opts{"order-id": "90"}); err == nil {
		t.Error("want the capture error")
	}
}

func TestOrdersLatest(t *testing.T) {
	useFakeSession(t)
	visited := useFakeOrders(t, orderPages())
	res := mustOK(t)(cmdOrders(nil, opts{"latest": "true"}))
	// Order 90 is newer by date; its detail total wins over the list header's.
	if res["orderId"] != 90.0 || res["total"] != 81.0 || res["status"] != "Prepared" || res["itemCount"] != 2 {
		t.Errorf("latest = %v", res)
	}
	if strings.Join(*visited, " ") != "/shop/myaccount/myorders /shop/myaccount/myorders/90" {
		t.Errorf("visited %v", *visited)
	}

	useFakeOrders(t, map[string]string{"/shop/myaccount/myorders": `[]`})
	res, err := cmdOrders(nil, opts{"latest": "true"})
	if err != nil || res["success"] != false {
		t.Errorf("no orders = %v, %v", res, err)
	}
	useFakeOrders(t, map[string]string{"/shop/myaccount/myorders": ordersListPage})
	if _, err := cmdOrders(nil, opts{"latest": "true"}); err == nil {
		t.Error("want the detail capture error")
	}
	useFakeOrders(t, map[string]string{})
	if _, err := cmdOrders(nil, opts{"latest": "true"}); err == nil {
		t.Error("want the list capture error")
	}
}

func TestCupboardAddLatest(t *testing.T) {
	useFakeSession(t)
	useFakeOrders(t, orderPages())
	f := useFakeSite(t, map[string]any{
		"GET /apis/ui/product/detail/1": `{"Product": {"PackageSize": "2L"}, "AdditionalAttributes": {"storageinstructions": "Keep refrigerated"}}`,
		"GET /apis/ui/product/detail/2": errors.New("lookup failed"),
	})
	file := filepath.Join(t.TempDir(), "cupboard.csv")

	res := mustOK(t)(cmdCupboard([]string{"add-latest"}, opts{"file": file, "dry-run": "true"}))
	if res["addedCount"] != 2 || res["dryRun"] != true {
		t.Errorf("dry run = %v", res)
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Error("dry run wrote the file")
	}

	res = mustOK(t)(cmdCupboard([]string{"add-latest"}, opts{"file": file}))
	if res["addedCount"] != 2 || res["orderId"] != 90.0 || len(res["lookupErrors"].([]any)) != 1 {
		t.Errorf("add-latest = %v", res)
	}
	b, _ := os.ReadFile(file)
	if !strings.Contains(string(b), "1,Milk 2L,2,order 90,") || !strings.Contains(string(b), ",2L,2000,ml,Keep refrigerated") {
		t.Errorf("file =\n%s", b)
	}
	if n := len(f.callsTo("GET /apis/ui/product/detail/1")); n != 2 {
		t.Errorf("product 1 looked up %d times over two runs, want once per run", n)
	}

	// Running again adds nothing.
	if res := mustOK(t)(cmdCupboard([]string{"add-latest"}, opts{"file": file})); res["addedCount"] != 0 {
		t.Errorf("second run = %v", res)
	}

	_, err := cmdCupboard([]string{"add-latest", "extra"}, opts{})
	mustUsage(t, err)
	_, err = cmdCupboard(nil, opts{})
	mustUsage(t, err)

	useFakeOrders(t, map[string]string{"/shop/myaccount/myorders": `[]`})
	if _, err := cmdCupboard([]string{"add-latest"}, opts{"file": file}); err == nil {
		t.Error("want an error when the latest order can't be read")
	}
	useFakeOrders(t, map[string]string{})
	if _, err := cmdCupboard([]string{"add-latest"}, opts{"file": file}); err == nil {
		t.Error("want the capture error")
	}
	if _, err := cmdCupboard([]string{"add-latest"}, opts{"file": filepath.Join(file, "not-a-dir", "c.csv")}); err == nil {
		t.Error("want a write error")
	}
}

const specialsCategories = `{"Categories": [{"NodeId": "specialsgroup", "Children": [
	{"NodeId": "s.half", "Description": "Half Price", "UrlFriendlyName": "half-price", "ProductCount": 3}]}]}`

func TestSpecialsAllLive(t *testing.T) {
	resetConfig(t, t.TempDir(), "")
	old := specialsPageDelay
	specialsPageDelay = 0
	t.Cleanup(func() { specialsPageDelay = old })
	f := useFakeSite(t, map[string]any{
		"GET /apis/ui/PiesCategoriesWithSpecials": specialsCategories,
		"POST /apis/ui/browse/category": `{"Bundles": [
			{"Products": [{"Stockcode": 1, "DisplayName": "Half", "Price": 5, "WasPrice": 10, "IsOnSpecial": true, "IsHalfPrice": true, "IsAvailable": true}]},
			{"Products": [{"Stockcode": 2, "DisplayName": "Small", "Price": 9, "WasPrice": 10, "IsOnSpecial": true, "IsAvailable": true}]}]}`,
		"GET /api/ui/v2/bootstrap": bootstrapJSON,
		"GET /apis/ui/Trolley":     trolleyJSON,
	})
	res := mustOK(t)(cmdSpecials(nil, opts{"all": "true", "min-discount": "20"}))
	if res["totalDeals"] != 2 || res["matched"] != 1 || res["storeId"] != 1234.0 || dig(res, "cache", "saved") != true {
		t.Errorf("live fetch = %v", res)
	}
	browses := len(f.callsTo("POST /apis/ui/browse/category"))
	if browses == 0 {
		t.Fatal("no browse calls")
	}

	// The second call is served from the cache it just saved.
	res = mustOK(t)(cmdSpecials(nil, opts{"all": "true", "limit": "1"}))
	if dig(res, "cache", "hit") != true || len(f.callsTo("POST /apis/ui/browse/category")) != browses || res["limited"] != true {
		t.Errorf("cached call = %v", res)
	}

	// Every page failing is an error; the categories failing too.
	f.routes["POST /apis/ui/browse/category"] = errors.New("down")
	if _, err := cmdSpecials(nil, opts{"all": "true", "refresh": "true"}); err == nil {
		t.Error("want an error when no page could be read")
	}
	f.routes["GET /apis/ui/PiesCategoriesWithSpecials"] = errors.New("down")
	if _, err := cmdSpecials(nil, opts{"all": "true", "max-pages": "1"}); err == nil {
		t.Error("want the categories error")
	}
	f.routes["GET /apis/ui/PiesCategoriesWithSpecials"] = `{"Categories": []}`
	if _, err := cmdSpecials(nil, opts{"all": "true", "refresh": "true"}); err == nil {
		t.Error("want an error when the specials group is missing")
	}

	for _, o := range []opts{
		{"term": "x"}, {"min-discount": "x"}, {"min-discount": "101"}, {"limit": "x"}, {"limit": "-1"},
		{"max-pages": "x"}, {"max-pages": "0"}, {"max-age": "x"}, {"max-age": "-1"},
	} {
		o["all"] = "true"
		_, err := cmdSpecials(nil, o)
		mustUsage(t, err)
	}
}
