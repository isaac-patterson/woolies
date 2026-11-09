package main

import (
	"errors"
	"testing"
)

const offersFixture = `{"data":[
 {"type":"STANDARD_OFFER","id":"a1","attributes":{"boostState":"NOT_BOOSTED","status":"ACTIVE","summary":{"bannerIds":["1005","1030"]},
  "display":{"section":"WEEKLY_BOOSTERS","heading":"Collect 60 points","description":{"summary":"Zucchinis (Minimum Spend $3)*"},"termsAndConditions":"long terms","primaryProductId":"170225"},
  "redemption":{"startDateTime":"2026-11-04T00:00:00.000+11:00","endDateTime":"2026-11-10T23:59:59.000+11:00"},
  "coupons":[{"attributes":{"reward":{"type":"POINTS_FIXED","value":60},"redemption":{"minimumSpend":300,"eligibleSalesChannels":["INSTORE","ONLINE_WEB"]}}}]}},
 {"type":"STANDARD_OFFER","id":"c3","attributes":{"boostState":"NOT_BOOSTED","status":"ACTIVE","summary":{"bannerIds":["88"]},
  "display":{"section":"DEFAULT","heading":"Say hello to CommBank","description":{"summary":"Bank promo"}},
  "redemption":{},"coupons":[]}},
 {"type":"STANDARD_OFFER","id":"b2","attributes":{"boostState":"BOOSTED","status":"ACTIVE","summary":{"bannerIds":["1005"]},
  "display":{"section":"DEFAULT","heading":"3% off eGift Card","description":{"summary":"Gift cards"}},
  "redemption":{"startDateTime":"2026-11-01T00:00:00.000+11:00","endDateTime":"2026-11-30T23:59:59.000+11:00"},"coupons":[]}}
]}`

func useFakeOffers(t *testing.T, bodies []capturedBody, err error) {
	t.Helper()
	captureOfferBodies = func(_ *session, target string) ([]capturedBody, error) {
		if target != homeURL+"/" {
			t.Errorf("target = %q", target)
		}
		return bodies, err
	}
	t.Cleanup(func() { captureOfferBodies = browserCaptureOfferBodies })
}

func TestOffersList(t *testing.T) {
	useFakeSession(t)
	useFakeOffers(t, []capturedBody{
		{"x/wx-rtl-offer-v2/offers", decode(t, offersFixture)},
		{"x/wx-rtl-offer-v2/offers", decode(t, offersFixture)}, // the page can fetch twice: no duplicates
	}, nil)
	res := mustOK(t)(cmdOffers(nil, opts{}))
	if res["total"] != 2 || res["boosted"] != 1 || res["count"] != 2 {
		t.Fatalf("counts: %v", res)
	}
	first := res["offers"].([]obj)[0]
	if first["boosted"] != false || first["heading"] != "Collect 60 points" || first["primaryProductId"] != "170225" {
		t.Fatalf("first offer: %v", first)
	}
	if first["minimumSpend"] != 3.0 {
		t.Fatalf("minimumSpend = %v, want 3 dollars", first["minimumSpend"])
	}
	if r := asObj(first["reward"]); r["type"] != "POINTS_FIXED" || r["value"] != 60.0 {
		t.Fatalf("reward: %v", first["reward"])
	}
	if _, ok := first["termsAndConditions"]; ok {
		t.Fatal("terms should be dropped")
	}
}

func TestOffersHidesPartnerPromosByDefault(t *testing.T) {
	useFakeSession(t)
	useFakeOffers(t, []capturedBody{{"x", decode(t, offersFixture)}}, nil)
	res := mustOK(t)(cmdOffers(nil, opts{}))
	if res["total"] != 2 || res["count"] != 2 || res["hidden"] != 1 {
		t.Fatalf("default: %v", res)
	}
	for _, of := range res["offers"].([]obj) {
		if of["id"] == "c3" {
			t.Fatal("partner promo should be hidden")
		}
	}
	res = mustOK(t)(cmdOffers(nil, opts{"all": "true"}))
	if res["total"] != 3 || res["count"] != 3 || res["hidden"] != 0 {
		t.Fatalf("--all: %v", res)
	}
}

func TestOffersFilters(t *testing.T) {
	useFakeSession(t)
	useFakeOffers(t, []capturedBody{{"x", decode(t, offersFixture)}}, nil)
	res := mustOK(t)(cmdOffers(nil, opts{"unboosted": "true"}))
	if res["count"] != 1 || res["boosted"] != 1 || res["total"] != 2 {
		t.Fatalf("unboosted: %v", res)
	}
	res = mustOK(t)(cmdOffers(nil, opts{"section": "default"}))
	if res["count"] != 1 || res["offers"].([]obj)[0]["id"] != "b2" {
		t.Fatalf("section: %v", res)
	}
	res = mustOK(t)(cmdOffers(nil, opts{"search": "ZUCCHINI"}))
	if res["count"] != 1 || res["offers"].([]obj)[0]["id"] != "a1" {
		t.Fatalf("search: %v", res)
	}
}

func TestOffersNoneAndError(t *testing.T) {
	useFakeSession(t)
	useFakeOffers(t, nil, nil)
	res, err := cmdOffers(nil, opts{})
	if err != nil || res["success"] != false {
		t.Fatalf("empty: %v %v", res, err)
	}
	useFakeOffers(t, nil, errors.New("boom"))
	if _, err := cmdOffers(nil, opts{}); err == nil {
		t.Fatal("want the capture error")
	}
}
