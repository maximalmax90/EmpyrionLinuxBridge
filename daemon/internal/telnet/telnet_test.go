package telnet

import (
	"net"
	"testing"
	"time"
)

func TestCommand_SilentServerIsError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	accepted := make(chan struct{})
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		close(accepted)

		<-time.After(2 * time.Second)
		conn.Close()
	}()

	c := New(ln.Addr().String(), "irrelevant")
	_, err = c.Command("", 1500*time.Millisecond)
	<-accepted
	if err == nil {
		t.Fatal("Command() против полностью молчащего сервера должен вернуть ошибку, а не (пустая строка, nil) — иначе watchdog.Check() никогда не увидит Hung=true")
	}
}

func TestCommand_RespondingServerOK(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.Write([]byte("Enter password:"))
		buf := make([]byte, 256)
		conn.Read(buf)
		conn.Write([]byte("= Version: test\n"))
	}()

	c := New(ln.Addr().String(), "pw")
	out, err := c.Command("", 2*time.Second)
	if err != nil {
		t.Fatalf("Command() против штатно отвечающего сервера не должен возвращать ошибку: %v", err)
	}
	if out == "" {
		t.Fatal("ожидался непустой login banner")
	}
}
