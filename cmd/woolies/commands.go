package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/cdp"
	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/storage"
	"github.com/chromedp/cdproto/target"
	"github.com/chromedp/chromedp"
)

type opts map[string]string

func (o opts) str(k, def string) string {
	if v, ok := o[k]; ok {
		return v
	}
	return def
}

func (o opts) num(k string, def int) (int, error) {
	v, ok := o[k]
	if !ok {
		return def, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return 0, usageErr("--%s expects a number, got %q", k, v)
	}
	return n, nil
}

type usageError struct{ msg string }

func (e *usageError) Error() string     { return e.msg }
func usageErr(f string, a ...any) error { return &usageError{fmt.Sprintf(f, a...)} }

func marshalStr(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

// ---- search / specials ----

// apiMaxPageSize is the most the search and browse APIs accept; anything larger gets a 400.
const apiMaxPageSize = 36

// pageSizeArg reads --page-size and caps it at the API limit. warning is set when it was capped.
func pageSizeArg(o opts) (size int, warning string, err error) {
	size, err = o.num("page-size", 8)
	if err != nil {
		return 0, "", err
	}
	if size < 1 {
		return 0, "", usageErr("--page-size must be at least 1")
	}
	if size > apiMaxPageSize {
		return apiMaxPageSize, fmt.Sprintf("--page-size %d was capped at %d, the most the site returns per page. Use --page for more.", size, apiMaxPageSize), nil
	}
	return size, "", nil
}

// addWarning puts a page-size warning on a result, if there is one.
func addWarning(out obj, warning string) obj {
	if warning != "" {
		out["warning"] = warning
	}
	return out
}

func searchBody(term string, page, size int, sort string, special bool) string {
	return marshalStr(obj{
		"searchTerm":       term,
		"pageNumber":       page,
		"pageSize":         size,
		"sortType":         sort,
		"location":         "/shop/search/products?searchTerm=" + url.QueryEscape(term),
		"formatObject":     marshalStr(obj{"name": term}),
		"isSpecial":        special,
		"isBundle":         false,
		"isMobile":         false,
		"filters":          []any{},
		"groupEdmVariants": false,
	})
}

func searchOne(term string, o opts) obj {
	page, err := o.num("page", 1)
	if err != nil {
		return obj{"success": false, "searchTerm": term, "error": err.Error()}
	}
	size, warning, err := pageSizeArg(o)
	if err != nil {
		return obj{"success": false, "searchTerm": term, "error": err.Error()}
	}
	_, special := o["special"]
	data, err := request(homeURL+"/apis/ui/Search/products", "POST",
		searchBody(term, page, size, o.str("sort", "TraderRelevance"), special))
	if err != nil {
		return obj{"success": false, "searchTerm": term, "error": err.Error()}
	}
	d := asObj(data)
	products := capN(flattenProducts(d["Products"]), size)
	if _, want := o["nutrition"]; want {
		addNutrition(products)
	}
	return addWarning(obj{
		"success":      true,
		"searchTerm":   term,
		"totalResults": orZero(d["SearchResultsCount"]),
		"products":     products,
	}, warning)
}

// addNutrition looks up each product's detail page and adds its per-100g nutrition (the keys of
// nutritionCols, only those the product lists). It costs one extra request per product. A product with
// no figures gets "nutrition": null so "none listed" is distinct from "not asked for", and a failed
// lookup puts the reason in "nutritionError" without failing the search.
func addNutrition(products []any) {
	for _, p := range products {
		m := asObj(p)
		data, err := request(homeURL+"/apis/ui/product/detail/"+url.PathEscape(str(m["stockcode"])), "GET", "")
		if err != nil {
			m["nutritionError"] = err.Error()
			continue
		}
		info := sizeFromProduct(asObj(data))
		nut := obj{}
		for i, name := range nutritionCols {
			if info.Nut[i] == "" {
				continue
			}
			if f, err := strconv.ParseFloat(info.Nut[i], 64); err == nil {
				nut[name] = f
			}
		}
		if len(nut) == 0 {
			m["nutrition"] = nil
			continue
		}
		m["nutrition"] = nut
	}
}

// capN trims a slice to n items; the API can return more than the requested page size.
func capN(a []any, n int) []any {
	if n > 0 && len(a) > n {
		return a[:n]
	}
	return a
}

func orZero(v any) any {
	if v == nil {
		return 0
	}
	return v
}

func cmdSearch(pos []string, o opts) (obj, error) {
	compact, err := compactWanted(o)
	if err != nil {
		return nil, err
	}
	out, err := searchTerms(pos, o)
	if err == nil && compact {
		compactResult(out)
	}
	return out, err
}

func searchTerms(pos []string, o opts) (obj, error) {
	if len(pos) == 0 {
		return nil, usageErr("search needs at least one term")
	}
	if len(pos) == 1 {
		return searchOne(pos[0], o), nil
	}
	results := []any{}
	allOK := true
	for _, t := range pos {
		r := searchOne(t, o)
		if r["success"] != true {
			allOK = false
		}
		results = append(results, r)
	}
	return obj{"success": allOK, "results": results}, nil
}

func cmdProduct(pos []string, _ opts) (obj, error) {
	if len(pos) == 0 {
		return nil, usageErr("product needs a stockcode")
	}
	data, err := request(homeURL+"/apis/ui/product/detail/"+url.PathEscape(pos[0]), "GET", "")
	if err != nil {
		return nil, err
	}
	return obj{"success": true, "product": data}, nil
}

func cmdSpecials(pos []string, o opts) (obj, error) {
	compact, err := compactWanted(o)
	if err != nil {
		return nil, err
	}
	out, err := specialsDispatch(pos, o)
	if err == nil && compact {
		compactResult(out)
	}
	return out, err
}

func specialsDispatch(_ []string, o opts) (obj, error) {
	if _, all := o["all"]; all {
		return cmdSpecialsAll(o)
	}
	if o.str("match", "") != "" {
		return nil, usageErr("--match works with specials --all")
	}
	category, term := o.str("category", ""), o.str("term", "")
	size, warning, err := pageSizeArg(o)
	if err != nil {
		return nil, err
	}
	page, err := o.num("page", 1)
	if err != nil {
		return nil, err
	}
	if category == "" && term == "" {
		return nil, usageErr("specials needs --all, --term <text> or --category <nodeId> (see `woolies categories`)")
	}
	if category != "" {
		// Browse ignores isSpecial, so fetch a full page and then filter.
		data, err := request(homeURL+"/apis/ui/browse/category", "POST",
			browseBody(category, o.str("category-slug", "specials"), page, apiMaxPageSize))
		if err != nil {
			return nil, err
		}
		d := asObj(data)
		src := d["Bundles"]
		if src == nil {
			src = d["Products"]
		}
		specials := []any{}
		for _, p := range flattenProducts(src) {
			if p.(obj)["isOnSpecial"] == true && len(specials) < size {
				specials = append(specials, p)
			}
		}
		return addWarning(obj{
			"success":      true,
			"category":     category,
			"totalResults": orZero(d["TotalRecordCount"]),
			"note":         "Category browse is filtered client-side to items on special from this page of results.",
			"products":     specials,
		}, warning), nil
	}
	data, err := request(homeURL+"/apis/ui/Search/products", "POST",
		searchBody(term, page, size, "TraderRelevance", true))
	if err != nil {
		return nil, err
	}
	d := asObj(data)
	specials := []any{}
	for _, p := range flattenProducts(d["Products"]) {
		if p.(obj)["isOnSpecial"] == true {
			specials = append(specials, p)
		}
	}
	return addWarning(obj{
		"success":      true,
		"searchTerm":   term,
		"totalResults": orZero(d["SearchResultsCount"]),
		"note":         "Filtered client-side to items actually on special from this page of results.",
		"products":     capN(specials, size),
	}, warning), nil
}

func cmdCategories(_ []string, _ opts) (obj, error) {
	data, err := request(homeURL+"/apis/ui/PiesCategoriesWithSpecials", "GET", "")
	if err != nil {
		return nil, err
	}
	return obj{"success": true, "categories": data}, nil
}

// ---- status / store ----

func bootstrapInfo() (boot obj, info obj, storeID any, err error) {
	b, err := request(homeURL+"/api/ui/v2/bootstrap", "GET", "")
	if err != nil {
		return nil, nil, nil, err
	}
	boot = asObj(b)
	info = asObj(boot["GetDeliveryInfoRequest"])
	if info == nil {
		info = obj{}
	}
	storeID = info["FulfilmentStoreId"]
	if storeID == nil {
		storeID = dig(boot, "ShopperRequest", "FulfilmentStoreId")
	}
	if cart, cerr := request(homeURL+"/apis/ui/Trolley", "GET", ""); cerr == nil {
		if id := dig(cart, "FulfilmentStoreId"); id != nil {
			storeID = id
		}
	}
	return boot, info, storeID, nil
}

func cmdStatus(_ []string, _ opts) (obj, error) {
	boot, info, storeID, err := bootstrapInfo()
	if err != nil {
		return nil, err
	}
	isGuest := dig(boot, "ShopperRequest", "IsGuest")
	out := obj{
		"success":          true,
		"loggedIn":         isGuest == false,
		"isGuest":          isGuest,
		"storeId":          storeID,
		"fulfilmentMethod": info["DeliveryMethod"],
		"minimumSpend":     info["MinimumSpend"],
		"reservedSlot":     nil,
		"profile":          profileDir(),
	}
	if tw := dig(info, "ReservedTime", "TimeWindow"); tw != nil && tw != "" {
		out["reservedSlot"] = obj{"date": dig(info, "ReservedDate", "DateText"), "window": tw}
	}
	if cart, cerr := request(homeURL+"/apis/ui/Trolley", "GET", ""); cerr == nil {
		c := trimCart(cart)
		out["cartLines"] = len(c["items"].([]any))
		out["cartQuantity"] = c["totalQuantity"]
		out["cartTotals"] = c["totals"]
	}
	return out, nil
}

func cmdStore(_ []string, o opts) (obj, error) {
	boot, info, storeID, err := bootstrapInfo()
	if err != nil {
		return nil, err
	}
	addr := asObj(info["Address"])
	result := obj{
		"success":           true,
		"fulfilmentStoreId": storeID,
		"fulfilmentMethod":  info["DeliveryMethod"],
		"isGuest":           dig(boot, "ShopperRequest", "IsGuest"),
		"address":           info["Address"],
		"reservedSlot":      nil,
		"minimumSpend":      info["MinimumSpend"],
	}
	if tw := dig(info, "ReservedTime", "TimeWindow"); tw != nil && tw != "" {
		result["reservedSlot"] = obj{"date": dig(info, "ReservedDate", "DateText"), "window": tw}
	}
	postcode := o.str("postcode", "")
	if postcode == "" && addr != nil {
		postcode = str(first(addr, "Postcode", "PostCode"))
	}
	if postcode == "" {
		postcode = defaultPostcode()
	}
	if postcode == "" {
		result["resolved"] = false
		result["note"] = "No postcode to look up store details: pass --postcode, or set postcode in the config file or WOOLIES_POSTCODE."
		return result, nil
	}
	list, lerr := request(homeURL+"/api/v3/ui/fulfilment/stores?postcode="+url.QueryEscape(postcode), "GET", "")
	if lerr == nil {
		for _, s := range asArr(list) {
			sm := asObj(s)
			if sm != nil && str(sm["FulfilmentStoreId"]) == str(storeID) {
				result["store"] = obj{
					"name":             sm["Description"],
					"address":          sm["AddressText"],
					"suburb":           sm["Suburb"],
					"postcode":         sm["Postcode"],
					"fulfilmentMethod": sm["FulfilmentMethod"],
				}
				if result["fulfilmentMethod"] == nil {
					result["fulfilmentMethod"] = sm["FulfilmentMethod"]
				}
				break
			}
		}
	}
	if result["store"] == nil {
		result["resolved"] = false
		result["note"] = fmt.Sprintf("Store ID %s was not found among stores near postcode %s. Only the ID is reliable.", str(storeID), postcode)
	}
	return result, nil
}

// ---- cart ----

func cmdCart(_ []string, o opts) (obj, error) {
	data, err := request(homeURL+"/apis/ui/Trolley", "GET", "")
	if err != nil {
		return nil, err
	}
	cart := trimCart(data)
	out := obj{"success": true, "cart": cart}
	if o.str("summary", "") == "true" {
		var minSpend float64
		if _, info, _, berr := bootstrapInfo(); berr == nil {
			minSpend = toFloat(info["MinimumSpend"])
		} else {
			out["minimumSpendError"] = berr.Error()
		}
		offers, _, oerr := loadOffers(false)
		if oerr != nil {
			out["offersError"] = oerr.Error()
		}
		out["summary"] = cartSummary(cart, minSpend, offers)
	}
	if ok, err := compactWanted(o); err != nil {
		return nil, err
	} else if ok {
		compactResult(out)
	}
	return out, nil
}

type cartItem struct {
	Stockcode int
	Quantity  int
}

func trolleyUpdate(items []cartItem) (obj, error) {
	list := make([]any, 0, len(items))
	for _, i := range items {
		list = append(list, obj{
			"stockcode":            i.Stockcode,
			"quantity":             i.Quantity,
			"source":               "ProductDetail",
			"diagnostics":          "0",
			"searchTerm":           nil,
			"evaluateRewardPoints": false,
			"offerId":              nil,
			"profileId":            nil,
			"priceLevel":           nil,
		})
	}
	upd, err := request(homeURL+"/api/v3/ui/trolley/update", "POST", marshalStr(obj{"items": list}))
	if err != nil {
		return nil, err
	}
	// The update response doesn't list the items, so read the cart back for the full picture.
	data, err := request(homeURL+"/apis/ui/Trolley", "GET", "")
	if err != nil {
		return obj{"success": true, "cart": trimCart(upd), "note": "updated, but could not read the cart back: " + err.Error()}, nil
	}
	return obj{"success": true, "cart": trimCart(data)}, nil
}

func stockcodeArg(s, what string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n <= 0 {
		return 0, usageErr("%s: stockcode must be a positive integer", what)
	}
	return n, nil
}

func qtyArg(s, what string) (int, error) {
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0, usageErr("%s: quantity must be a non-negative integer", what)
	}
	return n, nil
}

func cmdAdd(pos []string, _ opts) (obj, error) {
	if len(pos) == 0 {
		return nil, usageErr("add needs a stockcode")
	}
	sc, err := stockcodeArg(pos[0], "add")
	if err != nil {
		return nil, err
	}
	q := 1
	if len(pos) > 1 {
		if q, err = qtyArg(pos[1], "add"); err != nil {
			return nil, err
		}
	}
	if q < 1 {
		return nil, usageErr("add: quantity must be at least 1 (use `remove` to remove)")
	}
	return trolleyUpdate([]cartItem{{sc, q}})
}

func cmdAddMany(pos []string, _ opts) (obj, error) {
	if len(pos) == 0 {
		return nil, usageErr("add-many needs a JSON file path or - for stdin")
	}
	var raw []byte
	var err error
	if pos[0] == "-" {
		raw, err = io.ReadAll(os.Stdin)
	} else {
		raw, err = os.ReadFile(pos[0])
	}
	if err != nil {
		return nil, usageErr("add-many: %v", err)
	}
	var entries []struct {
		Stockcode json.Number  `json:"stockcode"`
		Quantity  *json.Number `json:"quantity"`
	}
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, usageErr("add-many: input is not a valid JSON array of {stockcode, quantity}")
	}
	if len(entries) == 0 {
		return nil, usageErr("add-many: expected a non-empty JSON array")
	}
	items := make([]cartItem, 0, len(entries))
	for i, e := range entries {
		what := fmt.Sprintf("add-many item %d", i)
		sc, err := stockcodeArg(e.Stockcode.String(), what)
		if err != nil {
			return nil, err
		}
		q := 1
		if e.Quantity != nil {
			if q, err = qtyArg(e.Quantity.String(), what); err != nil {
				return nil, err
			}
		}
		if q < 1 {
			return nil, usageErr("%s: quantity must be at least 1", what)
		}
		items = append(items, cartItem{sc, q})
	}
	return trolleyUpdate(items)
}

func cmdSetQty(pos []string, _ opts) (obj, error) {
	if len(pos) < 2 {
		return nil, usageErr("set-qty needs <stockcode> <quantity>")
	}
	sc, err := stockcodeArg(pos[0], "set-qty")
	if err != nil {
		return nil, err
	}
	q, err := qtyArg(pos[1], "set-qty")
	if err != nil {
		return nil, err
	}
	return trolleyUpdate([]cartItem{{sc, q}})
}

// cmdClear removes every line from the cart. --dry-run lists what would go without changing anything.
func cmdClear(_ []string, o opts) (obj, error) {
	data, err := request(homeURL+"/apis/ui/Trolley", "GET", "")
	if err != nil {
		return nil, err
	}
	cart := trimCart(data)
	var items []cartItem
	seen := map[int]bool{}
	for _, it := range cart["items"].([]any) {
		m := it.(obj)
		sc, err := strconv.Atoi(str(m["stockcode"]))
		if err != nil || sc <= 0 || seen[sc] {
			continue
		}
		seen[sc] = true
		items = append(items, cartItem{sc, 0})
	}
	if _, dry := o["dry-run"]; dry {
		return obj{"success": true, "dryRun": true, "wouldRemove": len(items), "cart": cart}, nil
	}
	if len(items) == 0 {
		return obj{"success": true, "removed": 0, "cart": cart}, nil
	}
	res, err := trolleyUpdate(items)
	if err != nil {
		return nil, err
	}
	res["removed"] = len(items)
	return res, nil
}

// cmdQuickAdd searches for a term and sets the top available first-party result in the cart.
// It always reports which product it picked. --dry-run picks but does not touch the cart.
func cmdQuickAdd(pos []string, o opts) (obj, error) {
	if len(pos) == 0 {
		return nil, usageErr("quick-add needs a search term, then an optional quantity")
	}
	q := 1
	terms := pos
	if len(pos) > 1 {
		if n, err := strconv.Atoi(pos[len(pos)-1]); err == nil {
			if n < 1 {
				return nil, usageErr("quick-add: quantity must be at least 1")
			}
			q, terms = n, pos[:len(pos)-1]
		}
	}
	term := strings.Join(terms, " ")
	res := searchOne(term, opts{"page-size": "8"})
	if res["success"] != true {
		return res, nil
	}
	var picked obj
	for _, p := range res["products"].([]any) {
		pm := p.(obj)
		if pm["isAvailable"] != false && pm["isMarketProduct"] != true {
			picked = pm
			break
		}
	}
	if picked == nil {
		return obj{"success": false, "searchTerm": term, "error": "no available Woolies-stocked product in the top results"}, nil
	}
	if _, dry := o["dry-run"]; dry {
		return obj{"success": true, "dryRun": true, "picked": picked, "quantity": q}, nil
	}
	sc, err := stockcodeArg(str(picked["stockcode"]), "quick-add")
	if err != nil {
		return nil, err
	}
	out, err := trolleyUpdate([]cartItem{{sc, q}})
	if err != nil {
		return nil, err
	}
	out["picked"] = picked
	out["quantity"] = q
	return out, nil
}

func cmdRemove(pos []string, _ opts) (obj, error) {
	if len(pos) == 0 {
		return nil, usageErr("remove needs a stockcode")
	}
	sc, err := stockcodeArg(pos[0], "remove")
	if err != nil {
		return nil, err
	}
	return trolleyUpdate([]cartItem{{sc, 0}})
}

// ---- addresses / timeslots (read-only) ----

func savedAddresses() ([]obj, error) {
	data, err := request(homeURL+"/apis/ui/Address", "GET", "")
	if err != nil {
		return nil, err
	}
	var out []obj
	for _, a := range asArr(dig(data, "Address")) {
		if m := asObj(a); m != nil {
			out = append(out, m)
		}
	}
	return out, nil
}

func cmdAddresses(_ []string, _ opts) (obj, error) {
	addrs, err := savedAddresses()
	if err != nil {
		return nil, err
	}
	list := []any{}
	for _, a := range addrs {
		list = append(list, obj{
			"addressId": a["AddressId"],
			"address":   a["AddressText"],
			"suburb":    a["SuburbName"],
			"postcode":  a["PostalCode"],
			"areaId":    a["AreaId"],
			"isPrimary": a["IsPrimary"],
		})
	}
	return obj{"success": true, "count": len(list), "addresses": list}, nil
}

// cmdTimeslots lists delivery windows for a saved address. Read-only: it never reserves a slot.
func cmdTimeslots(_ []string, o opts) (obj, error) {
	method := o.str("method", "Courier")
	if !strings.EqualFold(method, "Courier") && !strings.EqualFold(method, "Pickup") {
		return nil, usageErr("--method must be Courier or Pickup")
	}
	method = strings.ToUpper(method[:1]) + strings.ToLower(method[1:])
	addrs, err := savedAddresses()
	if err != nil {
		return nil, err
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no saved addresses on this account")
	}
	want := o.str("address", "")
	var addr obj
	for _, a := range addrs {
		if (want != "" && str(a["AddressId"]) == want) || (want == "" && a["IsPrimary"] == true) {
			addr = a
			break
		}
	}
	if addr == nil && want == "" {
		addr = addrs[0]
	}
	if addr == nil {
		return nil, usageErr("--address %s is not one of your saved addresses (see `woolies addresses`)", want)
	}
	q := url.Values{}
	q.Set("areaId", str(addr["AreaId"]))
	q.Set("addressId", str(addr["AddressId"]))
	q.Set("fulfilmentMethod", method)
	data, err := request(homeURL+"/api/v3/ui/fulfilment/windows?"+q.Encode(), "GET", "")
	if err != nil {
		return nil, err
	}
	_, all := o["all"]
	days := []any{}
	total := 0
	for _, d := range asArr(dig(data, "Days")) {
		dm := asObj(d)
		if dm == nil {
			continue
		}
		slots := []any{}
		for i, t := range asArr(dm["Times"]) {
			tm := asObj(t)
			if tm == nil {
				continue
			}
			if !all && tm["Available"] != true {
				continue
			}
			slots = append(slots, obj{
				"index":     i,
				"id":        tm["Id"],
				"window":    tm["TimeWindow"],
				"start":     tm["StartDateTime"],
				"end":       tm["EndDateTime"],
				"price":     tm["Price"],
				"salePrice": tm["SalePrice"],
				"available": tm["Available"],
				"reserved":  tm["IsReserved"],
				"express":   tm["IsExpress"],
				"ecoWindow": tm["IsEcoWindow"],
			})
		}
		total += len(slots)
		if len(slots) == 0 && !all {
			continue
		}
		days = append(days, obj{"date": dm["Date"], "available": dm["Available"], "closedText": dm["ClosedText"], "slots": slots})
	}
	out := obj{
		"success":   true,
		"method":    method,
		"addressId": addr["AddressId"],
		"slotCount": total,
		"days":      days,
		"readOnly":  "Lists windows only. It does not reserve a slot.",
	}
	if len(asArr(dig(data, "Days"))) == 0 {
		out["note"] = "The site returned no windows for this method. Pickup windows depend on the chosen store and may not be listed here."
	}
	return out, nil
}

// ---- orders (read-only) ----

var ordersURLRe = regexp.MustCompile(`(?i)/orders/api/orders`)

type capturedBody struct {
	url  string
	json any
}

// cmdOrders loads the orders page and reads the order JSON the page itself fetches.
// Address, contact and payment fields are never copied into the result.
func cmdOrders(_ []string, o opts) (obj, error) {
	orderID := o.str("order-id", "")
	latest := o.str("latest", "") == "true"
	if latest && orderID != "" {
		return nil, usageErr("--latest and --order-id cannot be combined")
	}
	active := o.str("active", "") == "true"
	if active && orderID != "" {
		return nil, usageErr("--active filters lists and --latest; it cannot be combined with --order-id")
	}
	if orderID != "" {
		if _, err := strconv.ParseUint(orderID, 10, 64); err != nil {
			return nil, usageErr("--order-id must be a number")
		}
	}
	limit, err := o.num("limit", 5)
	if err != nil {
		return nil, err
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 20 {
		limit = 20
	}

	s, err := getSession()
	if err != nil {
		return nil, err
	}

	if latest {
		return fetchLatestOrder(s, active)
	}

	target := homeURL + "/shop/myaccount/myorders"
	if orderID != "" {
		target += "/" + orderID
	}
	bodies, err := captureOrderBodies(s, target)
	if err != nil {
		return nil, err
	}
	if orderID != "" {
		out := orderDetail(bodies, orderID)
		if out["window"] == nil {
			// The detail response may lack the pickup window; the order list carries it.
			if listBodies, lerr := captureOrderBodies(s, homeURL+"/shop/myaccount/myorders"); lerr == nil {
				for _, h := range orderSummaries(listBodies) {
					if hm := asObj(h); str(hm["orderId"]) == orderID {
						out["window"] = hm["window"]
						if out["replacedBy"] == nil {
							out["replacedBy"] = hm["replacedBy"]
						}
					}
				}
			}
		}
		return out, nil
	}
	unique := orderSummaries(bodies)
	if active {
		unique = activeOrders(unique)
	}
	count := len(unique)
	if len(unique) > limit {
		unique = unique[:limit]
	}
	out := obj{"success": true, "count": count, "orders": unique}
	// Surface the pickup window of the newest listed order at the top level.
	if newest := newestOrder(unique); newest != nil && newest["window"] != nil {
		out["window"] = newest["window"]
	}
	return out, nil
}

// fetchLatestOrder returns the newest order with its items, regardless of status (the status
// the site reports can lag the app).
func fetchLatestOrder(s *session, active bool) (obj, error) {
	bodies, err := captureOrderBodies(s, homeURL+"/shop/myaccount/myorders")
	if err != nil {
		return nil, err
	}
	summaries := orderSummaries(bodies)
	if active {
		summaries = activeOrders(summaries)
	}
	newest := newestOrder(summaries)
	if newest == nil {
		return obj{"success": false, "note": "No orders found on the orders page."}, nil
	}
	id := str(newest["orderId"])
	detailBodies, err := captureOrderBodies(s, homeURL+"/shop/myaccount/myorders/"+id)
	if err != nil {
		return nil, err
	}
	out := orderDetail(detailBodies, id)
	for k, v := range newest { // list header is the baseline; detail summary fields win
		if cur, ok := out[k]; !ok || cur == nil {
			out[k] = v
		}
	}
	return out, nil
}

// captureOrderBodies loads target and returns the order JSON responses the page fetches.
// Tests swap it for a fake; the real one is browserCaptureOrderBodies.
var captureOrderBodies = browserCaptureOrderBodies

func browserCaptureOrderBodies(s *session, target string) ([]capturedBody, error) {
	var mu sync.Mutex
	var reqIDs []network.RequestID
	urls := map[network.RequestID]string{}
	chromedp.ListenTarget(s.ctx, func(ev any) {
		if e, ok := ev.(*network.EventResponseReceived); ok {
			if ordersURLRe.MatchString(e.Response.URL) && strings.Contains(strings.ToLower(e.Response.MimeType), "json") {
				mu.Lock()
				reqIDs = append(reqIDs, e.RequestID)
				urls[e.RequestID] = strings.SplitN(e.Response.URL, "?", 2)[0]
				mu.Unlock()
			}
		}
	})
	if err := chromedp.Run(s.ctx, network.Enable()); err != nil {
		return nil, err
	}
	if err := navigate(s.ctx, target, 60*time.Second); err != nil {
		return nil, err
	}
	// Wait for the page's order requests to arrive, then give stragglers a moment.
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(reqIDs)
		mu.Unlock()
		if n > 0 {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	time.Sleep(2500 * time.Millisecond)

	mu.Lock()
	ids := append([]network.RequestID(nil), reqIDs...)
	mu.Unlock()
	var bodies []capturedBody
	for _, id := range ids {
		var b []byte
		if err := chromedp.Run(s.ctx, chromedp.ActionFunc(func(c context.Context) error {
			var gerr error
			b, gerr = network.GetResponseBody(id).Do(c)
			return gerr
		})); err != nil {
			continue
		}
		var parsed any
		if json.Unmarshal(b, &parsed) == nil {
			bodies = append(bodies, capturedBody{urls[id], parsed})
		}
	}
	return bodies, nil
}

// orderDetail builds the single-order result (header fields plus items) from captured bodies.
func orderDetail(bodies []capturedBody, orderID string) obj {
	items := []any{}
	var summary obj
	for _, b := range bodies {
		if !strings.Contains(b.url, "/orders/"+orderID) && !strings.Contains(marshalStr(b.json), orderID) {
			continue
		}
		var arrs [][]any
		findArrays(b.json, func(x obj) bool { return hasKeyDeep(x, "stockcode", 4) }, &arrs)
		for _, arr := range arrs {
			for _, e := range arr {
				if em := asObj(e); em != nil {
					items = append(items, trimOrderItem(em))
				}
			}
		}
		if summary == nil {
			root := b.json
			if a := asArr(root); len(a) > 0 {
				root = a[0]
			}
			if rm := asObj(root); rm != nil && pick(rm, "orderid") != nil {
				summary = trimOrderSummary(rm)
			}
		}
	}
	seen := map[string]bool{}
	unique := []any{}
	for _, it := range items {
		im := it.(obj)
		k := str(im["stockcode"]) + "|" + str(im["qty"])
		if !seen[k] {
			seen[k] = true
			unique = append(unique, it)
		}
	}
	out := obj{"success": len(unique) > 0, "orderId": orderID, "itemCount": len(unique), "items": unique}
	maps.Copy(out, summary)
	if len(unique) == 0 {
		shapes, captured := []any{}, []any{}
		for _, b := range bodies {
			shapes = append(shapes, shapeOf(b.json, 4))
			captured = append(captured, b.url)
		}
		out["note"] = "No items parsed. Layout of the captured order JSON (key names only) is in `shape`."
		out["capturedUrls"] = captured
		out["shape"] = shapes
	}
	return out
}

// orderSummaries returns the de-duplicated order headers found in captured bodies, in page order.
func orderSummaries(bodies []capturedBody) []any {
	seen := map[string]bool{}
	unique := []any{}
	for _, b := range bodies {
		var arrs [][]any
		findArrays(b.json, func(x obj) bool { return pick(x, "orderid") != nil }, &arrs)
		for _, arr := range arrs {
			for _, e := range arr {
				if em := asObj(e); em != nil {
					x := trimOrderSummary(em)
					id := str(x["orderId"])
					if !seen[id] {
						seen[id] = true
						unique = append(unique, x)
					}
				}
			}
		}
	}
	markReplaced(unique)
	return unique
}

// ---- login ----

// cmdLogin opens a visible browser for manual sign-in and returns once its last window is closed.
func cmdLogin() (obj, error) {
	closeBrowser()
	s, err := startBrowser(false)
	if err != nil {
		return nil, err
	}
	sess = s
	// Go straight to the sign-in page (it redirects to the auth host with a fresh state token).
	if err := navigate(s.ctx, homeURL+"/shop/securelogin", 60*time.Second); err != nil {
		dbg("could not open login page: " + err.Error())
	}
	var landed string
	_ = chromedp.Run(s.ctx, chromedp.Sleep(4*time.Second), chromedp.Location(&landed))
	if u, perr := url.Parse(landed); perr == nil {
		dbg("login page landed on " + u.Host + u.Path)
	}
	fmt.Fprintln(os.Stderr, "A browser window is open. Sign in to Woolies, then close the window to finish.")
	// Ask the browser (not the tab) how many pages are open: a tab-level call blocks forever
	// once the tab is gone. Each poll has a timeout, and an exited browser also ends the wait.
	c := chromedp.FromContext(s.ctx)
	pid := c.Browser.Process().Pid
	for {
		time.Sleep(1500 * time.Millisecond)
		if !pidAlive(pid) {
			break
		}
		tctx, cancel := context.WithTimeout(cdp.WithExecutor(context.Background(), c.Browser), 3*time.Second)
		n, err := targetPages(tctx)
		cancel()
		if err == nil && n == 0 {
			break
		}
	}
	// Shut down gracefully (flushes cookies to the profile), then confirm the login actually
	// stuck by reading the session headlessly, rather than claiming success blindly.
	time.Sleep(1500 * time.Millisecond)
	closeBrowser()
	boot, _, _, err := bootstrapInfo()
	if err != nil {
		return nil, fmt.Errorf("browser closed, but could not verify the login: %w", err)
	}
	if dig(boot, "ShopperRequest", "IsGuest") != false {
		return obj{"success": false, "loggedIn": false, "error": "Browser closed but you are not signed in. Run `woolies login` again and make sure the page shows your account before closing the window."}, nil
	}
	return obj{"success": true, "loggedIn": true, "message": "Signed in."}, nil
}

// cmdLogout signs out by wiping the cookies and site data in the dedicated woolies profile,
// then confirms the session is a guest again. Nothing is read or printed from the cookies.
func cmdLogout(_ []string, _ opts) (obj, error) {
	s, err := getSession()
	if err != nil {
		return nil, err
	}
	if err := chromedp.Run(s.ctx, chromedp.ActionFunc(func(c context.Context) error {
		if err := network.ClearBrowserCookies().Do(c); err != nil {
			return err
		}
		return storage.ClearDataForOrigin(homeURL, "all").Do(c)
	})); err != nil {
		return nil, fmt.Errorf("could not clear the session: %w", err)
	}
	// The cached deals include this account's personalised offers.
	if err := os.Remove(specialsCachePath()); err != nil && !os.IsNotExist(err) {
		dbg("could not remove the specials cache: " + err.Error())
	}
	closeBrowser() // flush the cleared state to the profile
	boot, _, _, err := bootstrapInfo()
	if err != nil {
		return nil, fmt.Errorf("cleared the session, but could not verify: %w", err)
	}
	if dig(boot, "ShopperRequest", "IsGuest") != true {
		return obj{"success": false, "loggedIn": true, "error": "Still signed in after clearing the session."}, nil
	}
	return obj{"success": true, "loggedIn": false, "message": "Signed out."}, nil
}

// targetPages counts the open page tabs in the browser.
func targetPages(c context.Context) (int, error) {
	infos, err := target.GetTargets().Do(c)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, t := range infos {
		dbg(fmt.Sprintf("target type=%s url=%s", t.Type, t.URL))
		if t.Type == "page" {
			n++
		}
	}
	return n, nil
}
