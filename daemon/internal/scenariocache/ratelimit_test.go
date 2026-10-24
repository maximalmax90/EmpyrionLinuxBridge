package scenariocache

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestIPRateLimiter_AllowsUpToLimit(t *testing.T) {
	l := NewIPRateLimiter(3, time.Minute)
	for i := 0; i < 3; i++ {
		if !l.Allow("1.2.3.4") {
			t.Fatalf("request %d should be allowed", i)
		}
	}
	if l.Allow("1.2.3.4") {
		t.Fatal("4th request should be denied")
	}
}

func TestIPRateLimiter_SeparateIPsIndependent(t *testing.T) {
	l := NewIPRateLimiter(1, time.Minute)
	if !l.Allow("1.1.1.1") {
		t.Fatal("first IP's first request should be allowed")
	}
	if !l.Allow("2.2.2.2") {
		t.Fatal("second IP should have its own independent quota")
	}
	if l.Allow("1.1.1.1") {
		t.Fatal("first IP's second request should be denied")
	}
}

func TestIPRateLimiter_ResetsAfterWindow(t *testing.T) {
	l := NewIPRateLimiter(1, 10*time.Millisecond)
	if !l.Allow("1.2.3.4") {
		t.Fatal("first request should be allowed")
	}
	time.Sleep(20 * time.Millisecond)
	if !l.Allow("1.2.3.4") {
		t.Fatal("request after window expiry should be allowed again")
	}
}

func TestIPRateLimiter_Middleware_Returns429OverLimit(t *testing.T) {
	l := NewIPRateLimiter(1, time.Minute)
	h := l.Middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))

	req := httptest.NewRequest("GET", "/", nil)
	req.RemoteAddr = "5.6.7.8:12345"

	rec1 := httptest.NewRecorder()
	h.ServeHTTP(rec1, req)
	if rec1.Code != http.StatusOK {
		t.Fatalf("first request: got %d, want 200", rec1.Code)
	}

	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)
	if rec2.Code != http.StatusTooManyRequests {
		t.Fatalf("second request: got %d, want 429", rec2.Code)
	}
}
