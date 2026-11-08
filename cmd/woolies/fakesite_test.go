package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// A fake Woolies API for offline tests. Routes are keyed "METHOD /path" (query string
// dropped). A route's value is a JSON string (decoded fresh on every call), an error, or a
// func(body string) (any, error) for responses that depend on the request.

type apiCall struct{ method, path, query, body string }

type fakeSite struct {
	t      *testing.T
	routes map[string]any
	calls  []apiCall
}

func useFakeSite(t *testing.T, routes map[string]any) *fakeSite {
	t.Helper()
	f := &fakeSite{t: t, routes: routes}
	request = f.do
	t.Cleanup(func() { request = browserRequest })
	return f
}

func (f *fakeSite) do(url, method, body string) (any, error) {
	if method == "" {
		method = "GET"
	}
	path, query, _ := strings.Cut(strings.TrimPrefix(url, homeURL), "?")
	f.calls = append(f.calls, apiCall{method, path, query, body})
	route, ok := f.routes[method+" "+path]
	if !ok {
		return nil, errors.New("fake site: no route for " + method + " " + path)
	}
	switch r := route.(type) {
	case string:
		return decode(f.t, r), nil
	case error:
		return nil, r
	case func(string) (any, error):
		return r(body)
	}
	f.t.Fatalf("fake site: bad route value %T", route)
	return nil, nil
}

// callsTo returns the calls made to one route, in order.
func (f *fakeSite) callsTo(key string) []apiCall {
	var out []apiCall
	for _, c := range f.calls {
		if c.method+" "+c.path == key {
			out = append(out, c)
		}
	}
	return out
}

func decode(t *testing.T, s string) any {
	t.Helper()
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		t.Fatalf("bad JSON fixture: %v\n%s", err, s)
	}
	return v
}

// bodyOf decodes a request body sent to the fake site.
func bodyOf(t *testing.T, c apiCall) obj {
	t.Helper()
	return asObj(decode(t, c.body))
}

// useFakeSession makes getSession return a session without starting a browser.
func useFakeSession(t *testing.T) {
	t.Helper()
	sess = &session{}
	t.Cleanup(func() { sess = nil })
}

// useFakeOrders serves captured order JSON per page path (homeURL stripped).
func useFakeOrders(t *testing.T, pages map[string]string) *[]string {
	t.Helper()
	var visited []string
	captureOrderBodies = func(_ *session, target string) ([]capturedBody, error) {
		path := strings.TrimPrefix(target, homeURL)
		visited = append(visited, path)
		js, ok := pages[path]
		if !ok {
			return nil, errors.New("fake orders: no page " + path)
		}
		var bodies []capturedBody
		for _, b := range asArr(decode(t, js)) {
			m := asObj(b)
			bodies = append(bodies, capturedBody{str(m["url"]), m["json"]})
		}
		return bodies, nil
	}
	t.Cleanup(func() { captureOrderBodies = browserCaptureOrderBodies })
	return &visited
}

func mustUsage(t *testing.T, err error) {
	t.Helper()
	if _, ok := errors.AsType[*usageError](err); !ok {
		t.Fatalf("want a usage error, got %v", err)
	}
}

func mustOK(t *testing.T) func(obj, error) obj {
	t.Helper()
	return func(res obj, err error) obj {
		t.Helper()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if res["success"] != true {
			t.Fatalf("success = %v, result %v", res["success"], res)
		}
		return res
	}
}
