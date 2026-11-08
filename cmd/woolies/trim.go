package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"
)

type obj = map[string]any

func asObj(v any) obj {
	m, _ := v.(obj)
	return m
}

func asArr(v any) []any {
	a, _ := v.([]any)
	return a
}

// dig walks nested objects by key; returns nil if any step is missing.
func dig(v any, keys ...string) any {
	for _, k := range keys {
		m := asObj(v)
		if m == nil {
			return nil
		}
		v = m[k]
	}
	return v
}

func has(m obj, k string) bool { _, ok := m[k]; return ok }

// first returns the first non-nil value among the given keys.
func first(m obj, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok && v != nil {
			return v
		}
	}
	return nil
}

func sortedKeys(m obj) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func truthy(v any) bool {
	switch t := v.(type) {
	case bool:
		return t
	case nil:
		return false
	case float64:
		return t != 0
	case string:
		return t != ""
	}
	return true
}

func str(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		return fmt.Sprintf("%.0f", t)
	}
	return fmt.Sprint(v)
}

// ---- Products ----

func trimProduct(p obj) obj {
	onSpecial := truthy(p["IsOnSpecial"])
	out := obj{
		"stockcode":       p["Stockcode"],
		"displayName":     first(p, "DisplayName", "Name"),
		"packageSize":     p["PackageSize"],
		"price":           p["Price"],
		"cupString":       p["CupString"],
		"isOnSpecial":     onSpecial,
		"isAvailable":     p["IsAvailable"],
		"isMarketProduct": truthy(p["IsMarketProduct"]),
	}
	if onSpecial {
		out["wasPrice"] = p["WasPrice"]
	}
	if truthy(p["IsMarketProduct"]) {
		out["marketplace"] = "third-party marketplace listing"
	}
	return out
}

// rawProducts unpacks the site's product groups into the untrimmed products.
func rawProducts(groups any) []obj {
	out := []obj{}
	for _, g := range asArr(groups) {
		m := asObj(g)
		if m == nil {
			continue
		}
		if prods, ok := m["Products"].([]any); ok {
			for _, p := range prods {
				if pm := asObj(p); pm != nil {
					out = append(out, pm)
				}
			}
		} else if has(m, "Stockcode") {
			out = append(out, m)
		}
	}
	return out
}

func flattenProducts(groups any) []any {
	out := []any{}
	for _, p := range rawProducts(groups) {
		out = append(out, trimProduct(p))
	}
	return out
}

// ---- Cart ----

var cartLists = []string{
	"AvailableItems", "BundleItems", "ConflictedItems", "UnavailableItems", "RestrictedItems",
	"ExceededAtpLimitItems", "RestrictedByDeliveryMethodItems",
}

func trimCart(data any) obj {
	d := asObj(data)
	items := []any{}
	var collect func(node any, bucket string)
	collect = func(node any, bucket string) {
		switch n := node.(type) {
		case []any:
			for _, e := range n {
				collect(e, bucket)
			}
		case obj:
			if has(n, "Stockcode") {
				it := obj{}
				set := func(k string, v any) {
					if v != nil {
						it[k] = v
					}
				}
				set("stockcode", n["Stockcode"])
				set("name", first(n, "Name", "DisplayName", "Description"))
				set("qty", n["Quantity"])
				set("unitPrice", first(n, "Price", "ListPrice"))
				set("salePrice", n["SalePrice"])
				set("wasPrice", n["WasPrice"])
				if bucket != "AvailableItems" {
					it["status"] = bucket
				}
				items = append(items, it)
				return
			}
			for _, k := range sortedKeys(n) {
				collect(n[k], bucket)
			}
		}
	}
	for _, l := range cartLists {
		collect(d[l], l)
	}
	errs := []any{}
	for _, e := range asArr(d["Errors"]) {
		switch t := e.(type) {
		case string:
			errs = append(errs, t)
		case obj:
			if v := first(t, "Message", "Description"); v != nil {
				errs = append(errs, v)
			} else {
				b, _ := json.Marshal(t)
				s := string(b)
				if len(s) > 300 {
					s = s[:300]
				}
				errs = append(errs, s)
			}
		}
	}
	out := obj{
		"items":  items,
		"errors": errs,
	}
	if v := d["Totals"]; v != nil {
		out["totals"] = v
	}
	if v := d["TotalTrolleyItemQuantity"]; v != nil {
		out["totalQuantity"] = v
	}
	if v := dig(d, "DeliveryFee", "Total"); v != nil {
		out["deliveryFee"] = v
	}
	if v := d["FulfilmentStoreId"]; v != nil {
		out["FulfilmentStoreId"] = v
	}
	return out
}

// ---- Orders: generic JSON walking ----

// pick returns the value of the first key (case-insensitive) matching one of names, in preference order.
func pick(o obj, names ...string) any {
	for _, n := range names {
		for _, k := range sortedKeys(o) {
			if strings.EqualFold(k, n) {
				return o[k]
			}
		}
	}
	return nil
}

func hasKeyDeep(node any, name string, depth int) bool {
	if depth < 0 {
		return false
	}
	switch n := node.(type) {
	case obj:
		for k, v := range n {
			if strings.EqualFold(k, name) || hasKeyDeep(v, name, depth-1) {
				return true
			}
		}
	case []any:
		for _, v := range n {
			if hasKeyDeep(v, name, depth-1) {
				return true
			}
		}
	}
	return false
}

// findArrays collects arrays of objects in which at least one element satisfies test.
func findArrays(node any, test func(obj) bool, out *[][]any) {
	switch n := node.(type) {
	case []any:
		if len(n) > 0 {
			allObj, anyMatch := true, false
			for _, e := range n {
				m := asObj(e)
				if m == nil {
					allObj = false
					break
				}
				if test(m) {
					anyMatch = true
				}
			}
			if allObj && anyMatch {
				*out = append(*out, n)
			}
		}
		for _, e := range n {
			findArrays(e, test, out)
		}
	case obj:
		for _, k := range sortedKeys(n) {
			findArrays(n[k], test, out)
		}
	}
}

// flattenObj merges nested plain objects into one record; outer keys win, arrays skipped.
func flattenObj(node obj, depth int, out obj) obj {
	for _, k := range sortedKeys(node) {
		switch v := node[k].(type) {
		case obj, []any:
			_ = v
		default:
			if _, ok := out[k]; !ok {
				out[k] = v
			}
		}
	}
	if depth > 0 {
		for _, k := range sortedKeys(node) {
			if m := asObj(node[k]); m != nil {
				flattenObj(m, depth-1, out)
			}
		}
	}
	return out
}

// shapeOf returns key names only (never values), so a JSON layout can be inspected safely.
func shapeOf(node any, depth int) any {
	switch n := node.(type) {
	case []any:
		if len(n) == 0 {
			return []any{}
		}
		return []any{shapeOf(n[0], depth-1)}
	case obj:
		if depth <= 0 {
			return "{…}"
		}
		o := obj{}
		for k, v := range n {
			o[k] = shapeOf(v, depth-1)
		}
		return o
	case string:
		return "string"
	case float64:
		return "number"
	case bool:
		return "boolean"
	}
	return "null"
}

func trimOrderSummary(o obj) obj {
	var window any
	w := pick(o, "projecteddeliverytime", "deliverywindowstart")
	if wm := asObj(w); wm != nil {
		window = obj{"start": wm["StartTime"], "end": wm["EndTime"]}
	} else {
		window = w
	}
	return obj{
		"orderId":     pick(o, "orderid"),
		"status":      pick(o, "currentstatus", "status"),
		"created":     pick(o, "createddate"),
		"total":       pick(o, "total"),
		"method":      pick(o, "deliverymethod"),
		"pickupStore": pick(o, "pickupstorenumber"),
		"window":      window,
	}
}

func trimOrderItem(raw obj) obj {
	o := flattenObj(raw, 3, obj{})
	return obj{
		"stockcode": pick(o, "stockcode", "productid", "id"),
		"name":      pick(o, "displayname", "name", "description", "title"),
		"brand":     pick(o, "brand"),
		"qty":       pick(o, "quantity", "qty", "orderedquantity"),
		"lineTotal": pick(o, "total", "linetotal", "totalprice", "price"),
		"unitPrice": pick(o, "unitprice", "instorecupprice"),
	}
}

// newestOrder returns the most recent order header, whatever its status. It compares the
// created timestamps when every order has a parseable one, and otherwise the numeric order
// IDs (which grow over time). Returns nil for an empty list.
func newestOrder(orders []any) obj {
	var best obj
	var bestT time.Time
	allTimes := true
	for _, e := range orders {
		m := asObj(e)
		if m == nil {
			continue
		}
		if _, ok := parseOrderTime(m["created"]); !ok {
			allTimes = false
		}
	}
	var bestID uint64
	for _, e := range orders {
		m := asObj(e)
		if m == nil {
			continue
		}
		id, _ := strconv.ParseUint(str(m["orderId"]), 10, 64)
		if allTimes {
			t, _ := parseOrderTime(m["created"])
			if best == nil || t.After(bestT) || (t.Equal(bestT) && id > bestID) {
				best, bestT, bestID = m, t, id
			}
		} else if best == nil || id > bestID {
			best, bestID = m, id
		}
	}
	return best
}

func parseOrderTime(v any) (time.Time, bool) {
	s, ok := v.(string)
	if !ok || s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{time.RFC3339Nano, "2006-01-02T15:04:05.999999999", "2006-01-02T15:04:05", "2006-01-02"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
