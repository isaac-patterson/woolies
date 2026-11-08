package main

import "testing"

func TestNewestOrderByCreated(t *testing.T) {
	orders := []any{
		obj{"orderId": float64(100), "created": "2026-10-01T10:00:00", "status": "Collected"},
		obj{"orderId": float64(90), "created": "2026-11-02T09:00:00", "status": "Prepared"},
		obj{"orderId": float64(95), "created": "2026-10-20T09:00:00", "status": "Collected"},
	}
	got := newestOrder(orders)
	if got == nil || str(got["orderId"]) != "90" {
		t.Fatalf("want order 90 (newest created, status ignored), got %v", got)
	}
}

func TestNewestOrderFallsBackToID(t *testing.T) {
	orders := []any{
		obj{"orderId": float64(10), "created": "not a date"},
		obj{"orderId": float64(30), "created": nil},
		obj{"orderId": float64(20), "created": "2026-01-01"},
	}
	got := newestOrder(orders)
	if got == nil || str(got["orderId"]) != "30" {
		t.Fatalf("want order 30, got %v", got)
	}
}

func TestNewestOrderEmpty(t *testing.T) {
	if newestOrder(nil) != nil {
		t.Fatal("want nil")
	}
}
