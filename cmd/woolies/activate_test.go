package main

import (
	"errors"
	"slices"
	"testing"
)

func offerJSON(id, banner, state, cta, coupon string) string {
	return `{"type":"STANDARD_OFFER","id":"` + id + `","attributes":{"boostState":"` + state + `","summary":{"bannerIds":["` + banner + `"]},
 "display":{"section":"DEFAULT","heading":"Heading ` + id + `","description":{"summary":"Offer ` + id + `"},"callToAction":{"label":"` + cta + `"}},
 "redemption":{"endDateTime":"2026-11-10T23:59:59.000+11:00"},
 "coupons":[{"id":"` + coupon + `","attributes":{"reward":{"type":"POINTS_FIXED","value":60}}}]}}`
}

// w1 and w3 are unboosted Woolworths offers, w2 is already boosted, p1 is an unboosted partner
// offer and p2 has no Boost button.
var activateFixture = `{"data":[` + offerJSON("w1", "1005", "NOT_BOOSTED", "Boost", "c-w1") + `,` +
	offerJSON("w2", "1005", "BOOSTED", "Boost", "c-w2") + `,` +
	offerJSON("w3", "1030", "NOT_BOOSTED", "Boost", "c-w3") + `,` +
	offerJSON("p1", "88", "NOT_BOOSTED", "Boost", "c-p1") + `,` +
	offerJSON("p2", "89", "NOT_BOOSTED", "Discover", "c-p2") + `]}`

type boostCall struct {
	clientID, hash string
	coupons        []string
}

// setupActivate fakes the offers page, the customer hash and the boost call. reply decides the
// boost response from the coupon ids sent.
func setupActivate(t *testing.T, clientID string, reply func(coupons []string) (any, error)) *[]boostCall {
	t.Helper()
	useFakeSession(t)
	useFakeOffers(t, []capturedBody{{"x", decode(t, activateFixture)}}, nil)
	useFakeSite(t, map[string]any{"GET /api/v3/ui/shopper/rewards/crn/hash": `"hash-value"`})
	rewardsClientID = func(*session) (string, error) {
		if clientID == "" {
			return "", errors.New("no client id")
		}
		return clientID, nil
	}
	var calls []boostCall
	postBoost = func(_ *session, cid, hash string, coupons []string) (any, error) {
		calls = append(calls, boostCall{cid, hash, coupons})
		return reply(coupons)
	}
	t.Cleanup(func() { postBoost = browserPostBoost; rewardsClientID = browserRewardsClientID })
	return &calls
}

func allOK(coupons []string) (any, error) {
	out := []any{}
	for _, c := range coupons {
		out = append(out, obj{"success": true, "offer": obj{"id": c}, "code": 200.0})
	}
	return out, nil
}

func ids(list any) []string {
	var out []string
	for _, of := range list.([]obj) {
		out = append(out, str(of["id"]))
	}
	return out
}

func TestActivateNeedsIDs(t *testing.T) {
	_, err := cmdActivate(nil, opts{})
	mustUsage(t, err)
	_, err = cmdActivate([]string{"w1"}, opts{"all": "true"})
	mustUsage(t, err)
}

func TestActivateUnknownID(t *testing.T) {
	calls := setupActivate(t, "client", allOK)
	_, err := cmdActivate([]string{"nope"}, opts{"yes": "true"})
	mustUsage(t, err)
	if len(*calls) != 0 {
		t.Fatal("nothing should be sent for an unknown id")
	}
}

func TestActivateDryRunByDefault(t *testing.T) {
	calls := setupActivate(t, "client", allOK)
	res := mustOK(t)(cmdActivate([]string{"w1", "w2", "p2"}, opts{}))
	if len(*calls) != 0 {
		t.Fatal("without --yes nothing may be sent")
	}
	if res["dryRun"] != true || !slices.Equal(ids(res["wouldBoost"]), []string{"w1"}) {
		t.Fatalf("wouldBoost: %v", res)
	}
	if !slices.Equal(ids(res["alreadyBoosted"]), []string{"w2"}) || !slices.Equal(ids(res["notBoostable"]), []string{"p2"}) {
		t.Fatalf("skipped lists: %v", res)
	}
}

func TestActivateBoostsWithYes(t *testing.T) {
	calls := setupActivate(t, "client-1", allOK)
	res := mustOK(t)(cmdActivate([]string{"w1", "c-w3"}, opts{"yes": "true"})) // an offer id and a coupon id
	if len(*calls) != 1 {
		t.Fatalf("calls: %v", *calls)
	}
	c := (*calls)[0]
	if c.clientID != "client-1" || c.hash != "hash-value" || !slices.Equal(c.coupons, []string{"c-w1", "c-w3"}) {
		t.Fatalf("boost call: %+v", c)
	}
	if !slices.Equal(ids(res["boosted"]), []string{"w1", "w3"}) {
		t.Fatalf("boosted: %v", res["boosted"])
	}
}

func TestActivateAllOnlyWoolworthsUnboosted(t *testing.T) {
	calls := setupActivate(t, "client", allOK)
	res := mustOK(t)(cmdActivate(nil, opts{"all": "true", "yes": "true"}))
	if len(*calls) != 1 || !slices.Equal((*calls)[0].coupons, []string{"c-w1", "c-w3"}) {
		t.Fatalf("calls: %v", *calls)
	}
	if !slices.Equal(ids(res["alreadyBoosted"]), []string{"w2"}) {
		t.Fatalf("already: %v", res["alreadyBoosted"])
	}
}

func TestActivateReportsFailures(t *testing.T) {
	setupActivate(t, "client", func(coupons []string) (any, error) {
		return []any{
			obj{"success": true, "offer": obj{"id": "c-w1"}, "code": 200.0},
			obj{"success": false, "offer": obj{"id": "c-w3"}, "errorMsg": "Cannot be boosted", "code": 400.0},
		}, nil
	})
	res, err := cmdActivate(nil, opts{"all": "true", "yes": "true"})
	if err != nil || res["success"] != false {
		t.Fatalf("want a failed result, got %v %v", res, err)
	}
	failed := res["failed"].([]obj)
	if len(failed) != 1 || failed[0]["id"] != "w3" || failed[0]["reason"] != "Cannot be boosted" {
		t.Fatalf("failed: %v", failed)
	}
	if !slices.Equal(ids(res["boosted"]), []string{"w1"}) {
		t.Fatalf("boosted: %v", res["boosted"])
	}
}

func TestActivateErrors(t *testing.T) {
	calls := setupActivate(t, "", allOK)
	if _, err := cmdActivate([]string{"w1"}, opts{"yes": "true"}); err == nil || len(*calls) != 0 {
		t.Fatalf("missing client id must stop before sending: %v", err)
	}
	rewardsClientID = func(*session) (string, error) { return "client", nil }
	postBoost = func(*session, string, string, []string) (any, error) { return nil, errors.New("boom") }
	if _, err := cmdActivate([]string{"w1"}, opts{"yes": "true"}); err == nil {
		t.Fatal("want the boost error")
	}
}
