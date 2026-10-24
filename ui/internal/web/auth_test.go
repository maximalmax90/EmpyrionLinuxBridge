package web

import (
	"net/http"
	"testing"
)

func TestClientIP(t *testing.T) {
	t.Run("без доверия прокси — всегда RemoteAddr, заголовки игнорируются", func(t *testing.T) {
		r := &http.Request{RemoteAddr: "203.0.113.9:54321", Header: http.Header{
			"X-Real-Ip": {"198.51.100.1"},
		}}
		if got := clientIP(r, false); got != "203.0.113.9" {
			t.Errorf("clientIP = %q, хочу 203.0.113.9 (заголовок не должен учитываться)", got)
		}
	})
	t.Run("доверие прокси — X-Real-IP в приоритете", func(t *testing.T) {
		r := &http.Request{RemoteAddr: "127.0.0.1:12345", Header: http.Header{
			"X-Real-Ip":       {"198.51.100.1"},
			"X-Forwarded-For": {"198.51.100.2, 10.0.0.1"},
		}}
		if got := clientIP(r, true); got != "198.51.100.1" {
			t.Errorf("clientIP = %q, хочу 198.51.100.1", got)
		}
	})
	t.Run("доверие прокси, нет X-Real-IP — первый адрес из X-Forwarded-For", func(t *testing.T) {
		r := &http.Request{RemoteAddr: "127.0.0.1:12345", Header: http.Header{
			"X-Forwarded-For": {"198.51.100.2, 10.0.0.1"},
		}}
		if got := clientIP(r, true); got != "198.51.100.2" {
			t.Errorf("clientIP = %q, хочу 198.51.100.2 (первый — исходный клиент)", got)
		}
	})
	t.Run("доверие прокси, но заголовков нет — падение на RemoteAddr", func(t *testing.T) {
		r := &http.Request{RemoteAddr: "203.0.113.9:54321"}
		if got := clientIP(r, true); got != "203.0.113.9" {
			t.Errorf("clientIP = %q, хочу 203.0.113.9", got)
		}
	})
	t.Run("RemoteAddr без порта — возвращается как есть", func(t *testing.T) {
		r := &http.Request{RemoteAddr: "203.0.113.9"}
		if got := clientIP(r, false); got != "203.0.113.9" {
			t.Errorf("clientIP = %q, хочу 203.0.113.9", got)
		}
	})
}
