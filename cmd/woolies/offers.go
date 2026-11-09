package main

import (
	"context"
	"encoding/json"
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

// cmdOffers lists the signed-in account's active Everyday Rewards offers. Read-only: it never
// boosts anything.
func cmdOffers(_ []string, o opts) (obj, error) {
	includeAll := o.str("all", "") == "true"

	s, err := getSession()
	if err != nil {
		return nil, err
	}
	bodies, err := captureOfferBodies(s, homeURL+"/")
	if err != nil {
		return nil, err
	}
	everything := offerList(bodies)
	if len(everything) == 0 {
		return obj{"success": false, "note": "No offers came back. You may be signed out (run `woolies status`)."}, nil
	}
	// By default keep only offers for Woolworths shopping. The rest are partner promos
	// (home loans, banking, BIG W, health brands, gift cards).
	all := everything
	if !includeAll {
		all = all[:0:0]
		for _, of := range everything {
			if isWoolworthsOffer(of) {
				all = append(all, of)
			}
		}
	}

	boosted := 0
	for _, of := range all {
		if of["boosted"] == true {
			boosted++
		}
	}
	return obj{"success": true, "total": len(all), "boosted": boosted, "hidden": len(everything) - len(all), "offers": all}, nil
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
		cattrs := asObj(dig(asObj(coupons[0]), "attributes"))
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
