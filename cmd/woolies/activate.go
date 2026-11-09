package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/chromedp/cdproto/network"
	"github.com/chromedp/cdproto/runtime"
	"github.com/chromedp/chromedp"
)

const (
	boostURL   = "https://prod.api-wr.com/wx/v1/csl/customers/offers/boost"
	crnHashURL = homeURL + "/api/v3/ui/shopper/rewards/crn/hash"
)

// postBoost sends the boost call for coupon ids. Tests swap it for a fake; the real one is
// browserPostBoost. It returns the API's per-offer results.
var postBoost = browserPostBoost

// rewardsClientID returns the client_id header the Rewards page sends to the rewards service. It is
// not the same as the one the offers list uses, and it is read from the page, not kept in this
// program. Tests swap it for a fake; the real one is browserRewardsClientID.
var rewardsClientID = browserRewardsClientID

func browserRewardsClientID(s *session) (string, error) {
	var mu sync.Mutex
	id := ""
	chromedp.ListenTarget(s.ctx, func(ev any) {
		e, ok := ev.(*network.EventRequestWillBeSent)
		if !ok || e.Request.Method == "OPTIONS" || !strings.Contains(e.Request.URL, "/wx/v1/csl/customers/offers") {
			return
		}
		for k, v := range e.Request.Headers {
			if h, _ := v.(string); strings.EqualFold(k, "client_id") && h != "" {
				mu.Lock()
				id = h
				mu.Unlock()
			}
		}
	})
	if err := chromedp.Run(s.ctx, network.Enable()); err != nil {
		return "", err
	}
	if err := navigate(s.ctx, homeURL+"/shop/myaccount/woolworthsrewards", 60*time.Second); err != nil {
		return "", err
	}
	deadline := time.Now().Add(25 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		got := id
		mu.Unlock()
		if got != "" {
			return got, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
	return "", fmt.Errorf("could not read the rewards client id from the Rewards page, so nothing was boosted")
}

// cmdActivate boosts ("activates") Everyday Rewards offers. Boosting changes the account and
// can't be undone, so nothing is sent unless --yes is passed; without it the command only
// shows what it would boost.
func cmdActivate(pos []string, o opts) (obj, error) {
	all := o.str("all", "") == "true"
	if len(pos) == 0 && !all {
		return nil, usageErr("usage: woolies activate <offer id>... [--yes], or --all for every unboosted Woolworths offer (ids come from `woolies offers`)")
	}
	if len(pos) > 0 && all {
		return nil, usageErr("give offer ids or --all, not both")
	}
	yes := o.str("yes", "") == "true"

	s, err := getSession()
	if err != nil {
		return nil, err
	}
	bodies, err := captureOfferBodies(s, homeURL+"/")
	if err != nil {
		return nil, err
	}
	offers := offerList(bodies)
	if len(offers) == 0 {
		return obj{"success": false, "note": "No offers came back. You may be signed out (run `woolies status`)."}, nil
	}

	var chosen []obj
	if all {
		for _, of := range offers {
			if isWoolworthsOffer(of) {
				chosen = append(chosen, of)
			}
		}
	} else {
		seen := map[string]bool{}
		for _, id := range pos {
			of := findOffer(offers, id)
			if of == nil {
				return nil, usageErr("no active offer with id %q (see `woolies offers --all`)", id)
			}
			if !seen[str(of["id"])] {
				seen[str(of["id"])] = true
				chosen = append(chosen, of)
			}
		}
	}

	var toBoost, already, notBoostable []obj
	for _, of := range chosen {
		switch {
		case of["boosted"] == true:
			already = append(already, offerBrief(of))
		case of["boostable"] != true || of["couponId"] == nil:
			notBoostable = append(notBoostable, offerBrief(of))
		default:
			toBoost = append(toBoost, of)
		}
	}
	result := obj{"success": true, "alreadyBoosted": nonNil(already), "notBoostable": nonNil(notBoostable)}
	if len(toBoost) == 0 {
		result["boosted"] = []obj{}
		result["note"] = "Nothing to boost."
		return result, nil
	}
	if !yes {
		briefs := make([]obj, 0, len(toBoost))
		for _, of := range toBoost {
			briefs = append(briefs, offerBrief(of))
		}
		result["dryRun"] = true
		result["wouldBoost"] = briefs
		result["note"] = "Nothing was changed. Boosting can't be undone; pass --yes to boost these."
		return result, nil
	}

	clientID, err := rewardsClientID(s)
	if err != nil {
		return nil, err
	}
	hash, err := request(crnHashURL, "GET", "")
	if err != nil {
		return nil, err
	}
	hashCRN, _ := hash.(string)
	if hashCRN == "" {
		return nil, fmt.Errorf("could not read the rewards customer hash, so nothing was boosted")
	}
	couponIDs := make([]string, 0, len(toBoost))
	for _, of := range toBoost {
		couponIDs = append(couponIDs, str(of["couponId"]))
	}
	resp, err := postBoost(s, clientID, hashCRN, couponIDs)
	if err != nil {
		return nil, err
	}
	byCoupon := map[string]obj{}
	for _, r := range asArr(resp) {
		if m := asObj(r); m != nil {
			byCoupon[str(dig(m, "offer", "id"))] = m
		}
	}
	boosted := []obj{}
	failed := []obj{}
	for _, of := range toBoost {
		brief := offerBrief(of)
		r := byCoupon[str(of["couponId"])]
		if r != nil && r["success"] == true {
			boosted = append(boosted, brief)
			continue
		}
		reason := "no result returned for this offer"
		if r != nil {
			reason = str(first(r, "errorMsg", "message"))
			if reason == "" {
				reason = "boost failed (code " + str(r["code"]) + ")"
			}
		}
		brief["reason"] = reason
		failed = append(failed, brief)
	}
	result["boosted"] = boosted
	if len(failed) > 0 {
		result["success"] = false
		result["failed"] = failed
	}
	return result, nil
}

func nonNil(l []obj) []obj {
	if l == nil {
		return []obj{}
	}
	return l
}

// findOffer matches an offer by its id or its coupon id.
func findOffer(offers []obj, id string) obj {
	for _, of := range offers {
		if str(of["id"]) == id || str(of["couponId"]) == id {
			return of
		}
	}
	return nil
}

// offerBrief is the short form used in activate results.
func offerBrief(of obj) obj {
	return obj{"id": of["id"], "heading": of["heading"], "description": of["description"], "endsAt": of["endsAt"]}
}

// browserPostBoost makes the boost call from inside the page, as the site does: no cookies are
// sent, the account is identified by the hashcrn and client_id headers.
func browserPostBoost(s *session, clientID, hashCRN string, couponIDs []string) (any, error) {
	body, _ := json.Marshal(obj{"offerIds": couponIDs})
	argBody, _ := json.Marshal(string(body))
	argClient, _ := json.Marshal(clientID)
	argHash, _ := json.Marshal(hashCRN)
	js := fmt.Sprintf(`(async () => {
  const res = await fetch(%q, {
    method: "POST",
    headers: {
      "content-type": "application/json",
      "hashcrn": %s,
      "client_id": %s,
      "userlocaltime": String(-1 * new Date().getTimezoneOffset()),
    },
    body: %s,
  });
  return {ok: res.ok, status: res.status, text: await res.text()};
})()`, boostURL, argHash, argClient, argBody)
	var out struct {
		OK     bool   `json:"ok"`
		Status int    `json:"status"`
		Text   string `json:"text"`
	}
	tctx, cancel := context.WithTimeout(s.ctx, 60*time.Second)
	defer cancel()
	if err := chromedp.Run(tctx, chromedp.Evaluate(js, &out, func(p *runtime.EvaluateParams) *runtime.EvaluateParams {
		return p.WithAwaitPromise(true)
	})); err != nil {
		return nil, err
	}
	dbg(fmt.Sprintf("boost response %d: %.300s", out.Status, out.Text))
	var parsed any
	if err := json.Unmarshal([]byte(out.Text), &parsed); err != nil {
		t := out.Text
		if len(t) > 200 {
			t = t[:200]
		}
		return nil, fmt.Errorf("boost call returned %d with non-JSON body: %s", out.Status, strings.TrimSpace(t))
	}
	return parsed, nil // the API reports per-offer errors in its body, so a non-2xx is not an error here
}
