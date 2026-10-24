package modclient

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestAllStructuresCache(t *testing.T) {
	var hits atomic.Int32
	var fail atomic.Bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		if fail.Load() {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Write([]byte(`[{"id":1},{"id":2}]`))
	}))
	defer srv.Close()
	c := New(srv.URL)

	t.Run("reuses result within maxAge", func(t *testing.T) {
		ResetAllStructuresCache()
		hits.Store(0)
		for i := 0; i < 3; i++ {
			list, err := c.AllStructures(time.Minute)
			if err != nil || len(list) != 2 {
				t.Fatalf("got %v, %v", list, err)
			}
		}
		if n := hits.Load(); n != 1 {
			t.Fatalf("hits = %d, want 1", n)
		}
	})

	t.Run("maxAge 0 always fetches", func(t *testing.T) {
		ResetAllStructuresCache()
		hits.Store(0)
		c.AllStructures(time.Minute)
		c.AllStructures(0)
		c.AllStructures(0)
		if n := hits.Load(); n != 3 {
			t.Fatalf("hits = %d, want 3", n)
		}
	})

	t.Run("concurrent callers share one request", func(t *testing.T) {
		ResetAllStructuresCache()
		hits.Store(0)
		var wg sync.WaitGroup
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				c.AllStructures(time.Minute)
			}()
		}
		wg.Wait()
		if n := hits.Load(); n != 1 {
			t.Fatalf("hits = %d, want 1", n)
		}
	})

	t.Run("errors are not cached", func(t *testing.T) {
		ResetAllStructuresCache()
		hits.Store(0)
		fail.Store(true)
		if _, err := c.AllStructures(time.Minute); err == nil {
			t.Fatal("want error")
		}
		fail.Store(false)
		list, err := c.AllStructures(time.Minute)
		if err != nil || len(list) != 2 {
			t.Fatalf("got %v, %v", list, err)
		}
		if n := hits.Load(); n != 2 {
			t.Fatalf("hits = %d, want 2", n)
		}
	})
}

func TestAllStructuresMaxAgeBelowMissingGrace(t *testing.T) {

	if AllStructuresMaxAge >= 45*time.Second {
		t.Fatalf("AllStructuresMaxAge = %v must stay below structurebackup.missingGrace (45s)", AllStructuresMaxAge)
	}
}
