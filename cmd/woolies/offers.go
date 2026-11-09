package main

import (
	"context"
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/chromedp"
)

// offersURLRe matches the Everyday Rewards offers call the home page makes. It lives on a
// different host (prod.api-wr.com) and authenticates with headers the page sets itself, so
// the command reads the page's own response instead of calling it.
var offersURLRe = regexp.MustCompile(`(?i)/wx-rtl-offer-v2/offers`)

// captureOfferBodies loads target and returns the offers JSON the page fetches.
// Tests swap it for a fake; the real one is browserCaptureOfferBodies.
var captureOfferBodies = browserCaptureOfferBodies

func browserCaptureOfferBodies(s *session, target string) ([]capturedBody, error) {
	var mu sync.Mutex
	var reqIDs []network.RequestID
	urls := map[network.RequestID]string{}
	chromedp.ListenTarget(s.ctx, func(ev any) {
		if e, ok := ev.(*network.EventResponseReceived); ok {
			if offersURLRe.MatchString(e.Response.URL) && strings.Contains(strings.ToLower(e.Response.MimeType), "json") {
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
	time.Sleep(1500 * time.Millisecond)

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

// loadOffers reads the account's offers from the home page. hidden counts the partner promos
// dropped unless includeAll is set.
func loadOffers(includeAll bool) (offers []obj, hidden int, err error) {
	s, err := getSession()
	if err != nil {
		return nil, 0, err
	}
	bodies, err := captureOfferBodies(s, homeURL+"/")
	if err != nil {
		return nil, 0, err
	}
	everything := offerList(bodies)
	if len(everything) == 0 {
		return nil, 0, errNoOffers
	}
	// By default keep only offers for Woolworths shopping. The rest are partner promos
	// (home loans, banking, BIG W, health brands, gift cards).
	if includeAll {
		return everything, 0, nil
	}
	for _, of := range everything {
		if isWoolworthsOffer(of) {
			offers = append(offers, of)
		}
	}
	return offers, len(everything) - len(offers), nil
}

var errNoOffers = errors.New("no offers came back; you may be signed out (run `woolies status`)")

// cmdOffers lists the signed-in account's active Everyday Rewards offers. Read-only: it never
// boosts anything. With --cart it also checks them against the current cart.
func cmdOffers(_ []string, o opts) (obj, error) {
	includeAll := o.str("all", "") == "true"
	all, hidden, err := loadOffers(includeAll)
	if errors.Is(err, errNoOffers) {
		return obj{"success": false, "note": "No offers came back. You may be signed out (run `woolies status`)."}, nil
	}
	if err != nil {
		return nil, err
	}
	boosted := 0
	for _, of := range all {
		if of["boosted"] == true {
			boosted++
		}
	}
	out := obj{"success": true, "total": len(all), "boosted": boosted, "hidden": hidden, "offers": all}
	if o.str("cart", "") == "true" {
		reach, err := reachArg(o)
		if err != nil {
			return nil, err
		}
		data, err := request(homeURL+"/apis/ui/Trolley", "GET", "")
		if err != nil {
			return nil, err
		}
		out["cartCheck"] = offerEval(trimCart(data), all, reach)
	}
	return out, nil
}

// woolworthsBanners are the banner ids on offers for Woolworths supermarkets (and Metro).
// Partner offers carry other banners (BIG W, banking, gift cards and so on).
var woolworthsBanners = map[string]bool{"1005": true, "1030": true}

func isWoolworthsOffer(of obj) bool {
	for _, b := range asArr(of["bannerIds"]) {
		if woolworthsBanners[str(b)] {
			return true
		}
	}
	return false
}

// offerList trims the captured offers responses to one entry per offer, in the page's order.
func offerList(bodies []capturedBody) []obj {
	var out []obj
	seen := map[string]bool{}
	for _, b := range bodies {
		for _, item := range asArr(dig(b.json, "data")) {
			m := asObj(item)
			id := str(m["id"])
			if m == nil || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, trimOffer(m))
		}
	}
	return out
}

// trimOffer keeps what is useful for picking offers; the long terms text is dropped.
func trimOffer(m obj) obj {
	attrs := asObj(m["attributes"])
	display := asObj(attrs["display"])
	red := asObj(attrs["redemption"])
	out := obj{
		"id":          m["id"],
		"boosted":     attrs["boostState"] == "BOOSTED",
		"boostable":   dig(display, "callToAction", "label") == "Boost",
		"section":     display["section"],
		"heading":     display["heading"],
		"description": dig(display, "description", "summary"),
		"bannerIds":   dig(attrs, "summary", "bannerIds"),
		"startsAt":    red["startDateTime"],
		"endsAt":      red["endDateTime"],
	}
	if v := display["primaryProductId"]; v != nil {
		out["primaryProductId"] = v
	}
	// The reward, minimum spend and channels sit on the first coupon.
	var couponRed obj
	if coupons := asArr(attrs["coupons"]); len(coupons) > 0 {
		idx := 0
		if n, ok := attrs["currentCouponIndex"].(float64); ok && int(n) >= 0 && int(n) < len(coupons) {
			idx = int(n)
		}
		out["couponId"] = asObj(coupons[idx])["id"]
		cattrs := asObj(dig(asObj(coupons[idx]), "attributes"))
		couponRed = asObj(cattrs["redemption"])
		if reward := asObj(cattrs["reward"]); reward != nil {
			out["reward"] = obj{"type": reward["type"], "value": reward["value"]}
		}
	}
	if v, ok := first(couponRed, "minimumSpend").(float64); ok && v > 0 {
		out["minimumSpend"] = v / 100 // the API sends cents
	}
	if ch := asArr(first(couponRed, "eligibleSalesChannels")); len(ch) > 0 {
		out["channels"] = ch
	}
	return out
}
