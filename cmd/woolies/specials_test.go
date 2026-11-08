package main

import (
	"errors"
	"maps"
	"reflect"
	"testing"
)

func raw(code float64, name string, extra obj) obj {
	p := obj{"Stockcode": code, "DisplayName": name, "Price": 10.0, "WasPrice": 10.0, "IsAvailable": true}
	maps.Copy(p, extra)
	return p
}

func multibuy(qty, price float64) obj {
	return obj{"MultibuyData": obj{"Quantity": qty, "Price": price}}
}

func TestBestDeal(t *testing.T) {
	tests := []struct {
		name    string
		extra   obj
		kind    string
		percent int
		ok      bool
	}{
		{"no deal", obj{}, "", 0, false},
		{"half price", obj{"IsOnSpecial": true, "IsHalfPrice": true, "WasPrice": 20.0}, "half-price", 50, true},
		{"plain special", obj{"IsOnSpecial": true, "WasPrice": 12.5}, "special", 20, true},
		{"on special with no saving", obj{"IsOnSpecial": true}, "", 0, false},
		{"seasonal drop in was price", obj{"WasPrice": 12.5, "CentreTag": obj{"TagContentText": "Was $12.50 - 01/09/2026"}}, "price-drop", 20, true},
		{"lower shelf drop only in tag", obj{"CentreTag": obj{"TagContentText": "Was $20 - 04/10/2026"}}, "price-drop", 50, true},
		{"range was in tag", obj{"CentreTag": obj{"TagContentText": "Range was $12.50 - 11/08/2026"}}, "price-drop", 20, true},
		{"tag price below current", obj{"CentreTag": obj{"TagContentText": "Was $8 - 04/10/2026"}}, "", 0, false},
		{"multibuy tag text is not a was price", obj{"CentreTag": obj{"TagContentText": "2 for $24.00 - $12.00/100ML"}}, "", 0, false},
		{"multibuy with special flag off", obj{"CentreTag": multibuy(2, 16)}, "multibuy", 20, true},
		{"multibuy beats special", obj{"IsOnSpecial": true, "WasPrice": 11.0, "CentreTag": multibuy(2, 10)}, "multibuy", 50, true},
		{"special beats multibuy", obj{"IsOnSpecial": true, "WasPrice": 20.0, "CentreTag": multibuy(2, 18)}, "special", 50, true},
		{"multibuy dearer than single", obj{"CentreTag": multibuy(2, 24)}, "", 0, false},
		{"multibuy of one is not a deal", obj{"CentreTag": multibuy(1, 5)}, "", 0, false},
		{"zero price", obj{"Price": 0.0, "IsOnSpecial": true, "WasPrice": 5.0}, "", 0, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, ok := bestDeal(raw(1, "x", tt.extra))
			if ok != tt.ok || d.percent != tt.percent || (ok && d.kind != tt.kind) {
				t.Errorf("got (%q, %d, %v), want (%q, %d, %v)", d.kind, d.percent, ok, tt.kind, tt.percent, tt.ok)
			}
		})
	}
}

func TestTrimSpecial(t *testing.T) {
	half := obj{"IsOnSpecial": true, "IsHalfPrice": true, "WasPrice": 20.0}

	if _, ok := trimSpecial(raw(1, "x", obj{"IsOnSpecial": true, "WasPrice": 20.0, "IsAvailable": false})); ok {
		t.Error("kept an unavailable product")
	}
	if _, ok := trimSpecial(raw(1, "x", obj{"IsOnSpecial": true, "WasPrice": 20.0, "IsMarketProduct": true})); ok {
		t.Error("kept a marketplace product")
	}

	half["AdditionalAttributes"] = obj{
		"piesdepartmentnamesjson": `["Dinner","Freezer"]`,
		"piescategorynamesjson":   []any{"Ice Cream"},
	}
	d, ok := trimSpecial(raw(6088226, "Ice Cream", half))
	if !ok {
		t.Fatal("dropped a half price product")
	}
	if d["dealType"] != "half-price" || d["discountPercent"] != 50 {
		t.Errorf("deal = %v %v", d["dealType"], d["discountPercent"])
	}
	if !reflect.DeepEqual(d["departments"], []string{"Dinner", "Freezer"}) {
		t.Errorf("departments = %v", d["departments"])
	}
	if !reflect.DeepEqual(d["categories"], []string{"Ice Cream"}) {
		t.Errorf("categories = %v", d["categories"])
	}

	drop, ok := trimSpecial(raw(2, "Mince", obj{"CentreTag": obj{"TagContentText": "Was $12.50 - 04/10/2026"}}))
	if !ok {
		t.Fatal("dropped a price drop")
	}
	if drop["wasPrice"] != 12.5 || drop["wasDate"] != "04/10/2026" {
		t.Errorf("price drop was = %v, date = %v", drop["wasPrice"], drop["wasDate"])
	}
}

func TestSpecialsFilter(t *testing.T) {
	deal := func(percent int, depts ...string) obj {
		return obj{"discountPercent": percent, "departments": append([]string{}, depts...)}
	}
	tests := []struct {
		name   string
		filter specialsFilter
		deal   obj
		want   bool
	}{
		{"no filter", specialsFilter{}, deal(10, "Beauty"), true},
		{"below minimum", specialsFilter{minDiscount: 25}, deal(20, "Pantry"), false},
		{"at minimum", specialsFilter{minDiscount: 20}, deal(20, "Pantry"), true},
		{"food keeps food", specialsFilter{food: true}, deal(10, "Pantry"), true},
		{"food drops mixed tags", specialsFilter{food: true}, deal(10, "Beauty", "Lunch Box"), false},
		{"food keeps untagged", specialsFilter{food: true}, deal(10), true},
		{"department ignores case", specialsFilter{departments: []string{"freezer"}}, deal(10, "Freezer"), true},
		{"department misses", specialsFilter{departments: []string{"Freezer"}}, deal(10, "Pantry"), false},
		{"department partial name", specialsFilter{departments: []string{"meat"}}, deal(10, "Poultry, Meat & Seafood"), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.filter.match(tt.deal); got != tt.want {
				t.Errorf("match = %v, want %v", got, tt.want)
			}
		})
	}
}

func onSpecial(code float64, name string) obj {
	return raw(code, name, obj{"IsOnSpecial": true, "WasPrice": 20.0})
}

func TestCollectSpecials(t *testing.T) {
	a, b := specialsList{ID: "a", Name: "A"}, specialsList{ID: "b", Name: "B"}
	pages := map[string][][]obj{
		"a": {
			{onSpecial(1, "one"), raw(2, "no deal", nil)},
			{onSpecial(3, "three")},
			{onSpecial(1, "one")}, // a repeat ends the list
			{onSpecial(99, "never reached")},
		},
		"b": {
			{onSpecial(3, "three"), onSpecial(4, "four")}, // 3 was already seen in A
		},
	}
	var calls []string
	fetch := func(l specialsList, page int) ([]obj, error) {
		calls = append(calls, l.ID)
		if page > len(pages[l.ID]) {
			return nil, nil
		}
		return pages[l.ID][page-1], nil
	}
	deals, reports, errs := collectSpecials([]specialsList{a, b}, 10, 0, fetch)
	if len(errs) != 0 {
		t.Fatalf("errs = %v", errs)
	}
	var names []string
	for _, d := range deals {
		names = append(names, d["displayName"].(string))
	}
	if want := []string{"one", "three", "four"}; !reflect.DeepEqual(names, want) {
		t.Errorf("deals = %v, want %v", names, want)
	}
	if want := []string{"a", "a", "a", "b", "b"}; !reflect.DeepEqual(calls, want) {
		t.Errorf("calls = %v, want %v", calls, want)
	}
	if got := reports[0].(obj)["pages"]; got != 3 {
		t.Errorf("list A pages = %v, want 3", got)
	}
}

func TestCollectSpecialsStopsAtMaxPages(t *testing.T) {
	calls := 0
	fetch := func(_ specialsList, page int) ([]obj, error) {
		calls++
		return []obj{onSpecial(float64(page), "p")}, nil
	}
	_, reports, _ := collectSpecials([]specialsList{{Name: "A"}}, 2, 0, fetch)
	if calls != 2 {
		t.Errorf("calls = %d, want 2", calls)
	}
	if reports[0].(obj)["truncated"] != true {
		t.Errorf("report = %v, want truncated", reports[0])
	}
}

func TestCollectSpecialsKeepsGoingAfterAFailedPage(t *testing.T) {
	fetch := func(l specialsList, page int) ([]obj, error) {
		if l.Name == "A" {
			return nil, errors.New("boom")
		}
		if page == 1 {
			return []obj{onSpecial(1, "one")}, nil
		}
		return nil, nil
	}
	deals, _, errs := collectSpecials([]specialsList{{Name: "A"}, {Name: "B"}}, 5, 0, fetch)
	if len(deals) != 1 {
		t.Errorf("deals = %d, want 1", len(deals))
	}
	if len(errs) != 1 {
		t.Errorf("errs = %v, want one", errs)
	}
}

func TestSpecialsLists(t *testing.T) {
	cats := obj{"Categories": []any{
		obj{"NodeId": "1_x", "Children": []any{obj{"NodeId": "wrong", "ProductCount": 5.0}}},
		obj{"NodeId": "specialsgroup", "Children": []any{
			obj{"NodeId": "s.half", "Description": "Half Price", "UrlFriendlyName": "half-price", "ProductCount": 10.0},
			obj{"NodeId": "s.empty", "Description": "Empty", "UrlFriendlyName": "empty", "ProductCount": 0.0},
			obj{"NodeId": "s.edlp", "Description": "EDLP", "UrlFriendlyName": "everyday-low-price", "ProductCount": 9.0},
		}},
	}}
	got, err := specialsLists(cats)
	if err != nil {
		t.Fatal(err)
	}
	want := []specialsList{{"s.half", "Half Price", "half-price"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("lists = %v, want %v", got, want)
	}
	if _, err := specialsLists(obj{"Categories": []any{}}); err == nil {
		t.Error("expected an error when the specials group is missing")
	}
}

func TestSortDeals(t *testing.T) {
	deals := []obj{
		{"discountPercent": 20, "displayName": "b"},
		{"discountPercent": 50, "displayName": "z"},
		{"discountPercent": 20, "displayName": "a"},
	}
	sortDeals(deals)
	var names []string
	for _, d := range deals {
		names = append(names, d["displayName"].(string))
	}
	if want := []string{"z", "a", "b"}; !reflect.DeepEqual(names, want) {
		t.Errorf("order = %v, want %v", names, want)
	}
}
