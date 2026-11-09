package main

import (
	"fmt"
	"math"
	"strings"
)

// ---- compact output ----

// compactLine renders a trimmed product, special or cart line as one pipe-separated line:
// stockcode | name | size | price | cup string | flags | xQty.
func compactLine(m obj) string {
	price := toFloat(m["price"])
	if price == 0 {
		price = cartPrice(m)
	}
	ps := ""
	if price > 0 {
		ps = fmt.Sprintf("$%.2f", price)
		if m["priceIsEstimate"] == true {
			ps = "~" + ps
		}
	}
	var flags []string
	if m["isOnSpecial"] == true {
		f := "SPECIAL"
		if was := toFloat(m["wasPrice"]); was > 0 {
			f += fmt.Sprintf(" (was $%.2f)", was)
		}
		flags = append(flags, f)
	}
	if dt := str(m["dealType"]); dt != "" && m["isOnSpecial"] != true {
		flags = append(flags, strings.ToUpper(dt))
	}
	if p := toFloat(m["discountPercent"]); p > 0 {
		flags = append(flags, fmt.Sprintf("-%.0f%%", p))
	}
	if m["isAvailable"] == false {
		flags = append(flags, "UNAVAILABLE")
	}
	if st := str(m["status"]); st != "" {
		flags = append(flags, "UNAVAILABLE:"+st)
	}
	if m["isVariableWeight"] == true {
		flags = append(flags, "VARIABLE-WEIGHT")
	}
	if m["isMarketProduct"] == true {
		flags = append(flags, "MARKETPLACE")
	}
	parts := []string{
		str(m["stockcode"]),
		str(first(m, "displayName", "name")),
		str(m["packageSize"]),
		ps,
		str(m["cupString"]),
		strings.Join(flags, ", "),
	}
	if q := m["qty"]; q != nil {
		parts = append(parts, "x"+str(q))
	}
	return strings.Join(parts, " | ")
}

// compactResult replaces product and cart-item arrays in a result with one-line strings under
// "lines". It follows the nesting search (results) and cart (cart) use.
func compactResult(out obj) {
	for _, key := range []string{"products", "items"} {
		arr, ok := out[key].([]any)
		if !ok {
			continue
		}
		lines := make([]any, 0, len(arr))
		for _, e := range arr {
			if m := asObj(e); m != nil {
				lines = append(lines, compactLine(m))
			}
		}
		delete(out, key)
		out["lines"] = lines
		out["lineFormat"] = "stockcode | name | size | price (~ = estimate) | cup string | flags | xqty"
	}
	for _, e := range asArr(out["results"]) {
		if m := asObj(e); m != nil {
			compactResult(m)
		}
	}
	if c := asObj(out["cart"]); c != nil {
		compactResult(c)
	}
}

// ---- cart summary and offer checks ----

func round2(f float64) float64 { return math.Round(f*100) / 100 }

// cartTotal reads the cart total in dollars from a trimmed cart.
func cartTotal(cart obj) float64 {
	t := asObj(cart["totals"])
	return toFloat(first(t, "Total", "TotalAmount", "SubTotal"))
}

// cartSavings is the total savings: the site's figure when the totals carry one, else the sum of
// (was price - price) x qty over the lines.
func cartSavings(cart obj) (float64, string) {
	if v, ok := first(asObj(cart["totals"]), "Savings", "TotalSavings", "SavingsAmount", "TotalSaving").(float64); ok {
		return v, "site"
	}
	sum := 0.0
	for _, e := range asArr(cart["items"]) {
		it := asObj(e)
		if was, p := toFloat(it["wasPrice"]), cartPrice(it); was > p && p > 0 {
			sum += (was - p) * toFloat(it["qty"])
		}
	}
	return round2(sum), "computed from wasPrice on each line"
}

// offerEval classifies each offer against the cart. reach is the biggest gap in dollars still
// counted as "within reach".
func offerEval(cart obj, offers []obj, reach float64) obj {
	total := cartTotal(cart)
	lineSpend := map[string]float64{}
	for _, e := range asArr(cart["items"]) {
		it := asObj(e)
		lineSpend[str(it["stockcode"])] += cartPrice(it) * toFloat(it["qty"])
	}
	qualifies, within, far, notOnline := []any{}, []any{}, []any{}, []any{}
	warnings := []any{}
	points := 0.0
	var breakdown []any
	bestMult, multName := 0.0, ""
	for _, of := range offers {
		min := toFloat(of["minimumSpend"])
		spend := total
		if pid := str(of["primaryProductId"]); pid != "" {
			spend = lineSpend[pid] // a product offer counts spend on that product
		}
		rewardType := str(dig(of, "reward", "type"))
		value := toFloat(dig(of, "reward", "value"))
		e := obj{
			"id": of["id"], "heading": of["heading"], "boosted": of["boosted"] == true,
			"reward": of["reward"], "minimumSpend": min,
		}
		if chans := asArr(of["channels"]); len(chans) > 0 {
			online := false
			for _, c := range chans {
				online = online || strings.Contains(strings.ToUpper(str(c)), "ONLINE")
			}
			if !online {
				notOnline = append(notOnline, e)
				continue
			}
		}
		gap := round2(min - spend)
		switch {
		case total <= 0:
			e["gap"] = math.Max(gap, 0)
			far = append(far, e) // an empty cart earns nothing, even from offers with no minimum
		case gap <= 0:
			qualifies = append(qualifies, e)
			if of["boosted"] != true {
				warnings = append(warnings, fmt.Sprintf("%q qualifies but is not boosted (run `woolies activate %s`).", str(of["heading"]), str(of["id"])))
				continue
			}
			switch rewardType {
			case "POINTS_MULTIPLIER":
				if value > bestMult {
					bestMult, multName = value, str(of["heading"])
				}
			case "POINTS_FIXED":
				points += value
				breakdown = append(breakdown, obj{"offer": of["heading"], "points": value})
			}
		case gap <= reach:
			e["gap"] = gap
			within = append(within, e)
			if of["boosted"] == true {
				warnings = append(warnings, fmt.Sprintf("Add $%.2f more to unlock %q (%s %v).", gap, str(of["heading"]), rewardType, value))
			}
		default:
			e["gap"] = gap
			far = append(far, e)
		}
	}
	if bestMult > 0 {
		pts := math.Floor(total * bestMult)
		points += pts
		breakdown = append([]any{obj{"offer": multName, "points": pts, "note": fmt.Sprintf("%vx on the cart total, base points included", bestMult)}}, breakdown...)
	} else {
		base := math.Floor(total)
		points += base
		breakdown = append([]any{obj{"offer": "base points", "points": base, "note": "1 point per dollar"}}, breakdown...)
	}
	return obj{
		"cartTotal": total, "qualifies": qualifies, "withinReach": within, "farAway": far,
		"notOnline": notOnline, "estimatedPoints": points, "pointsBreakdown": breakdown,
		"reachWithin": reach, "warnings": warnings,
		"note": "Estimate: counts only boosted offers whose minimum spend is met; only the best multiplier counts; fixed offers add up. Spend is the cart total, or the line spend for product offers. Delivery fees and exclusions are not modelled.",
	}
}

func reachArg(o opts) (float64, error) {
	n, err := o.num("reach", 30)
	if err != nil {
		return 0, err
	}
	if n < 0 {
		return 0, usageErr("--reach must be 0 or more dollars")
	}
	return float64(n), nil
}

// cartSummary builds the --summary view: total, savings, counts, shortfall to the pickup
// minimum and to each boosted offer's minimum spend.
func cartSummary(cart obj, minimumSpend float64, offers []obj) obj {
	total := cartTotal(cart)
	savings, src := cartSavings(cart)
	out := obj{
		"total":          total,
		"totalSavings":   savings,
		"savingsSource":  src,
		"itemCount":      cart["totalQuantity"],
		"lineCount":      len(asArr(cart["items"])),
		"minimumSpend":   minimumSpend,
		"shortfallToMin": round2(math.Max(0, minimumSpend-total)),
	}
	short := []any{}
	for _, of := range offers {
		min := toFloat(of["minimumSpend"])
		if of["boosted"] != true || min <= 0 || str(of["primaryProductId"]) != "" {
			continue
		}
		short = append(short, obj{"id": of["id"], "heading": of["heading"], "minimumSpend": min, "shortfall": round2(math.Max(0, min-total)), "reward": of["reward"]})
	}
	out["boostedOfferShortfalls"] = short
	return out
}
