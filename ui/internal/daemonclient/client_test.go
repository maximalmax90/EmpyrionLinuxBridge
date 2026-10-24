package daemonclient

import (
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestCircuitBreaker_SkipsWhileUnreachable(t *testing.T) {

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	ln.Close()

	c := New("http://"+addr, "tok", "")

	if c.Unreachable() {
		t.Fatal("свежий клиент не должен считаться недоступным до первой попытки")
	}

	if _, err := c.Health(); err == nil {
		t.Fatal("ожидали ошибку — порт закрыт")
	}
	if !c.Unreachable() {
		t.Fatal("после сетевого сбоя Unreachable() должен стать true")
	}

	start := time.Now()
	_, err = c.Health()
	elapsed := time.Since(start)
	if !errors.Is(err, ErrDaemonUnreachable) {
		t.Fatalf("ожидали ErrDaemonUnreachable, получили %v", err)
	}
	if elapsed > 500*time.Millisecond {
		t.Fatalf("breaker не сработал — вторая попытка заняла %v (должна быть почти мгновенной)", elapsed)
	}
}

func TestCircuitBreaker_RecoversAfterBackoff(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("{}"))
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "")
	c.reachMu.Lock()
	c.unreachable = true
	c.lastProbeAt = time.Now().Add(-unreachableProbeBackoff - time.Second)
	c.reachMu.Unlock()

	if _, err := c.Health(); err != nil {
		t.Fatalf("ожидали успешный запрос после истечения backoff, получили: %v", err)
	}
	if c.Unreachable() {
		t.Fatal("после успешного ответа Unreachable() должен стать false")
	}
}

func TestCircuitBreaker_HTTPErrorIsNotUnreachable(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := New(srv.URL, "tok", "")
	if _, err := c.Health(); err == nil {
		t.Fatal("ожидали ошибку от 500-ответа")
	}
	if c.Unreachable() {
		t.Fatal("HTTP-ошибка ответа не должна помечать демон недоступным — демон явно ответил")
	}
}

func TestFilterMounts_NilVsEmpty(t *testing.T) {
	mounts := []MountInfo{{Mountpoint: "/"}, {Mountpoint: "/server"}}

	if got := FilterMounts(mounts, nil); len(got) != len(mounts) {
		t.Fatalf("nil (никогда не сохранялось) должен показывать все маунты, получили %d из %d", len(got), len(mounts))
	}
	if got := FilterMounts(mounts, []string{}); len(got) != 0 {
		t.Fatalf("явно пустой список (все галки сняты) должен скрывать все маунты, получили %d", len(got))
	}
	if got := FilterMounts(mounts, []string{"/server"}); len(got) != 1 || got[0].Mountpoint != "/server" {
		t.Fatalf("ожидали только /server, получили %+v", got)
	}
}
