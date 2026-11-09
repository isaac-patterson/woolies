package main

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// specialsPageDelay is the pause between page requests (a var so tests can skip it).
var specialsPageDelay = 250 * time.Millisecond

const (
	specialsGroupID  = "specialsgroup"
	defaultMaxPages  = 60 // per list; the largest list needs about 45
	defaultDealLimit = 200
)

// skipSpecialLists holds the lists under the specials group that aren't discounts.
var skipSpecialLists = map[string]bool{"everyday-low-price": true, "bundles": true}

// nonFoodDepartments drives --food. A product is dropped if any of its departments is listed,
// because the site also tags non-food under food departments (sunscreen under "Lunch Box").
var nonFoodDepartments = map[string]bool{
	"beauty": true, "personal care": true, "health & wellness": true, "cleaning & maintenance": true,
	"baby": true, "pet": true, "electronics": true, "home & lifestyle": true,
	"beer, wine & spirits": true,
}

type specialsList struct{ ID, Name, Slug string }

// specialsLists reads the browsable lists under the specials group from the categories response.
func specialsLists(categories any) ([]specialsList, error) {
	for _, c := range asArr(dig(categories, "Categories")) {
		m := asObj(c)
		if m == nil || str(m["NodeId"]) != specialsGroupID {
			continue
		}
		var lists []specialsList
		for _, k := range asArr(m["Children"]) {
			km := asObj(k)
			count, _ := km["ProductCount"].(float64)
			if km == nil || count == 0 || skipSpecialLists[str(km["UrlFriendlyName"])] {
				continue
			}
			lists = append(lists, specialsList{str(km["NodeId"]), str(km["Description"]), str(km["UrlFriendlyName"])})
		}
		if len(lists) == 0 {
			return nil, fmt.Errorf("the specials group has no lists with products")
		}
		return lists, nil
	}
	return nil, fmt.Errorf("no %q node in the categories response; the site may have changed", specialsGroupID)
}

// stringList reads a value that the site sends either as a JSON array or as a string holding one.
func stringList(v any) []string {
	out := []string{}
	switch t := v.(type) {
	case string:
		_ = json.Unmarshal([]byte(t), &out)
	case []any:
		for _, e := range t {
			out = append(out, str(e))
		}
	}
	return out
}

// wasTagRe matches the old price in a price-drop tag: "Was $9 - 04/10/2026", "Range was $12 - 11/08/2026".
var wasTagRe = regexp.MustCompile(`(?i)\bwas \$([0-9]+(?:\.[0-9]+)?)(?:\s*-\s*([0-9]{2}/[0-9]{2}/[0-9]{4}))?`)

type deal struct {
	kind    string
	percent int
	was     float64 // the old price, for a price drop
	wasDate string  // the date beside it in the tag, exactly as the site shows it
}

// bestDeal works out the largest discount on a raw product. The site marks deals three ways:
//   - a special has IsOnSpecial set and a higher WasPrice.
//   - a price drop (Lower Shelf, Seasonal) leaves IsOnSpecial false. Its old price is in the tag
//     text, and WasPrice may just repeat the current price.
//   - a multibuy leaves IsOnSpecial false and Price at full price, with the offer in MultibuyData.
func bestDeal(p obj) (deal, bool) {
	price, _ := p["Price"].(float64)
	if price <= 0 {
		return deal{}, false
	}
	var d deal
	best := 0.0
	was, _ := p["WasPrice"].(float64)
	if truthy(p["IsOnSpecial"]) {
		if was > price {
			best = (was - price) / was * 100
			d.kind = "special"
			if truthy(p["IsHalfPrice"]) {
				d.kind = "half-price"
			}
		}
	} else {
		if m := wasTagRe.FindStringSubmatch(str(dig(p, "CentreTag", "TagContentText"))); m != nil {
			if tagWas, err := strconv.ParseFloat(m[1], 64); err == nil && tagWas > was {
				was = tagWas
			}
			d.wasDate = m[2]
		}
		if was > price {
			best = (was - price) / was * 100
			d.kind, d.was = "price-drop", was
		}
	}
	mb := asObj(dig(p, "CentreTag", "MultibuyData"))
	mbPrice, _ := mb["Price"].(float64)
	mbQty, _ := mb["Quantity"].(float64)
	if mbQty >= 2 && mbPrice > 0 {
		if off := (price - mbPrice/mbQty) / price * 100; off > best {
			best = off
			d = deal{kind: "multibuy"}
		}
	}
	d.percent = int(math.Round(best))
	return d, d.percent > 0
}

// trimSpecial trims a raw product and adds its deal. ok is false if it has no real discount,
// isn't available, or is a third-party marketplace listing.
func trimSpecial(p obj) (obj, bool) {
	d, ok := bestDeal(p)
	if !ok || p["IsAvailable"] == false || truthy(p["IsMarketProduct"]) {
		return nil, false
	}
	out := trimProduct(p)
	out["dealType"] = d.kind
	out["discountPercent"] = d.percent
	if d.kind == "price-drop" {
		out["wasPrice"] = d.was
		if d.wasDate != "" {
			out["wasDate"] = d.wasDate
		}
	}
	attrs := asObj(p["AdditionalAttributes"])
	out["departments"] = stringList(attrs["piesdepartmentnamesjson"])
	out["categories"] = stringList(attrs["piescategorynamesjson"])
	if mb := asObj(dig(p, "CentreTag", "MultibuyData")); mb != nil {
		out["multibuy"] = obj{"quantity": mb["Quantity"], "price": mb["Price"]}
	}
	return out, true
}

type specialsFilter struct {
	minDiscount int
	food        bool
	departments []string
}

func (f specialsFilter) match(p obj) bool {
	if p["discountPercent"].(int) < f.minDiscount {
		return false
	}
	depts := p["departments"].([]string)
	if f.food {
		for _, d := range depts {
			if nonFoodDepartments[strings.ToLower(d)] {
				return false
			}
		}
	}
	if len(f.departments) == 0 {
		return true
	}
	// Partial matching, because the flag splits on commas and department names can contain them:
	// "Poultry, Meat & Seafood" becomes "Poultry" and "Meat & Seafood", and both still match.
	for _, want := range f.departments {
		for _, d := range depts {
			if strings.Contains(strings.ToLower(d), strings.ToLower(want)) {
				return true
			}
		}
	}
	return false
}

// collectSpecials pages through every list, one request at a time, and returns each deal once.
// A list ends on an empty page or a page with nothing new, as sponsored products can repeat and
// the site's totals don't line up exactly with its pages. A failed page ends that list only, so
// the caller still gets the rest and can report what's missing.
func collectSpecials(lists []specialsList, maxPages int, delay time.Duration,
	fetch func(l specialsList, page int) ([]obj, error)) (deals []obj, reports []any, errs []string) {
	seen := map[string]bool{}
	firstRequest := true
	for _, l := range lists {
		inList := map[string]bool{}
		pages, added := 0, 0
		truncated := false
		for page := 1; ; page++ {
			if page > maxPages {
				truncated = true
				break
			}
			if !firstRequest {
				time.Sleep(delay)
			}
			firstRequest = false
			products, err := fetch(l, page)
			if err != nil {
				errs = append(errs, fmt.Sprintf("%s page %d: %v", l.Name, page, err))
				break
			}
			pages++
			fresh := 0
			for _, p := range products {
				sc := str(p["Stockcode"])
				if inList[sc] {
					continue
				}
				inList[sc] = true
				fresh++
				if seen[sc] {
					continue
				}
				seen[sc] = true
				if d, ok := trimSpecial(p); ok {
					deals = append(deals, d)
					added++
				}
			}
			if fresh == 0 {
				break
			}
		}
		report := obj{"name": l.Name, "pages": pages, "products": len(inList), "newDeals": added}
		if truncated {
			report["truncated"] = true
		}
		reports = append(reports, report)
	}
	return deals, reports, errs
}

// sortDeals puts the biggest discount first, then orders by name so output is repeatable.
func sortDeals(deals []obj) {
	sort.SliceStable(deals, func(i, j int) bool {
		a, b := deals[i]["discountPercent"].(int), deals[j]["discountPercent"].(int)
		if a != b {
			return a > b
		}
		return str(deals[i]["displayName"]) < str(deals[j]["displayName"])
	})
}

func browseBody(category, slug string, page, size int) string {
	u := "/shop/browse/" + slug
	return marshalStr(obj{
		"categoryId":   category,
		"pageNumber":   page,
		"pageSize":     size,
		"sortType":     "TraderRelevance",
		"url":          u,
		"location":     u,
		"formatObject": marshalStr(obj{"name": "Specials"}),
		"isSpecial":    true,
		"isBundle":     false,
		"isMobile":     false,
		"filters":      []any{},
	})
}

// fetchAllSpecials walks the site's specials lists live. This is the slow path, about 2 minutes.
func fetchAllSpecials(maxPages int) ([]obj, []any, []string, error) {
	categories, err := request(homeURL+"/apis/ui/PiesCategoriesWithSpecials", "GET", "")
	if err != nil {
		return nil, nil, nil, err
	}
	lists, err := specialsLists(categories)
	if err != nil {
		return nil, nil, nil, err
	}
	deals, reports, errs := collectSpecials(lists, maxPages, specialsPageDelay, func(l specialsList, page int) ([]obj, error) {
		data, err := request(homeURL+"/apis/ui/browse/category", "POST", browseBody(l.ID, l.Slug, page, apiMaxPageSize))
		if err != nil {
			return nil, err
		}
		d := asObj(data)
		src := d["Bundles"]
		if src == nil {
			src = d["Products"]
		}
		return rawProducts(src), nil
	})
	return deals, reports, errs, nil
}

func cmdSpecialsAll(o opts) (obj, error) {
	if o.str("term", "") != "" || o.str("category", "") != "" {
		return nil, usageErr("specials --all can't be combined with --term or --category")
	}
	minDiscount, err := o.num("min-discount", 0)
	if err != nil {
		return nil, err
	}
	if minDiscount < 0 || minDiscount > 100 {
		return nil, usageErr("--min-discount must be between 0 and 100")
	}
	limit, err := o.num("limit", defaultDealLimit)
	if err != nil {
		return nil, err
	}
	if limit < 0 {
		return nil, usageErr("--limit must be 0 (no limit) or more")
	}
	maxPages, err := o.num("max-pages", defaultMaxPages)
	if err != nil {
		return nil, err
	}
	if maxPages < 1 {
		return nil, usageErr("--max-pages must be at least 1")
	}
	var nameRe *regexp.Regexp
	if m := o.str("match", ""); m != "" {
		nameRe, err = regexp.Compile("(?i)" + m)
		if err != nil {
			return nil, usageErr("--match is not a valid regular expression: %v", err)
		}
	}
	_, food := o["food"]
	filter := specialsFilter{minDiscount: minDiscount, food: food}
	for d := range strings.SplitSeq(o.str("department", ""), ",") {
		if d = strings.TrimSpace(d); d != "" {
			filter.departments = append(filter.departments, d)
		}
	}

	maxAgeHours, err := o.num("max-age", defaultCacheMaxHours)
	if err != nil {
		return nil, err
	}
	if maxAgeHours < 0 {
		return nil, usageErr("--max-age must be 0 (always fetch) or more hours")
	}
	_, refresh := o["refresh"]
	_, customPages := o["max-pages"] // an explicit page cap asks for a live walk

	var deals []obj
	var reports []any
	var errs []string
	var storeID any
	var cacheInfo obj
	now := time.Now()
	if !refresh && !customPages {
		c, err := loadSpecialsCache()
		switch {
		case err != nil:
			dbg("specials cache not used: " + err.Error())
		case cacheFresh(c.FetchedAt, now, time.Duration(maxAgeHours)*time.Hour):
			deals, reports, storeID = c.Deals, c.Lists, c.StoreID
			cacheInfo = obj{
				"hit":        true,
				"fetchedAt":  c.FetchedAt.Format(time.RFC3339),
				"ageMinutes": int(now.Sub(c.FetchedAt).Minutes()),
			}
		}
	}
	if cacheInfo == nil {
		deals, reports, errs, err = fetchAllSpecials(maxPages)
		if err != nil {
			return nil, err
		}
		if len(deals) == 0 && len(errs) > 0 {
			return nil, fmt.Errorf("could not read any specials: %s", strings.Join(errs, "; "))
		}
		if _, _, id, err := bootstrapInfo(); err == nil {
			storeID = id
		}
		cacheInfo = obj{"hit": false, "fetchedAt": now.Format(time.RFC3339), "saved": false}
		if cacheable(deals, reports, errs) {
			c := &specialsCache{FetchedAt: now, StoreID: storeID, Lists: reports, Deals: deals}
			if err := saveSpecialsCache(c); err != nil {
				cacheInfo["error"] = err.Error()
			} else {
				cacheInfo["saved"] = true
			}
		}
	}

	matched := []obj{}
	for _, d := range deals {
		if nameRe != nil && !nameRe.MatchString(str(d["displayName"])) {
			continue
		}
		if filter.match(d) {
			matched = append(matched, d)
		}
	}
	sortDeals(matched)
	products := []any{}
	for _, d := range matched {
		products = append(products, d)
	}
	out := obj{
		"success":    true,
		"cache":      cacheInfo,
		"storeId":    storeID,
		"totalDeals": len(deals),
		"matched":    len(matched),
		"lists":      reports,
		"products":   capN(products, limit),
	}
	if limit > 0 && len(matched) > limit {
		out["limited"] = true
	}
	if len(errs) > 0 {
		out["incomplete"] = true
		out["errors"] = errs
	}
	return out, nil
}
