package web

import (
	"testing"
	"time"
)

func TestIPRateLimiter_AllowsUpToLimit(t *testing.T) {
	l := newIPRateLimiter(3, time.Minute)
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
	l := newIPRateLimiter(1, time.Minute)
	if !l.Allow("1.1.1.1") {
		t.Fatal("first IP's first request should be allowed")
	}
	if !l.Allow("2.2.2.2") {
		t.Fatal("second IP should have its own independent quota")
	}
}
