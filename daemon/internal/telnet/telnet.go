package telnet

import (
	"fmt"
	"net"
	"strings"
	"time"
)

type Client struct {
	Addr string
	Pwd  string
}

func New(addr, pwd string) *Client {
	return &Client{Addr: addr, Pwd: pwd}
}

const quietPeriod = 400 * time.Millisecond

func (c *Client) Command(cmd string, timeout time.Duration) (string, error) {
	conn, err := net.DialTimeout("tcp", c.Addr, 5*time.Second)
	if err != nil {
		return "", fmt.Errorf("telnet dial: %w", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(timeout)
	conn.SetDeadline(deadline)

	prompt, err := readQuiet(conn)
	if err != nil {
		return "", fmt.Errorf("telnet read prompt: %w", err)
	}

	if prompt == "" {
		return "", fmt.Errorf("telnet: empty response on connect — server is not responding")
	}
	if _, err := conn.Write([]byte(c.Pwd + "\n")); err != nil {
		return "", fmt.Errorf("telnet send password: %w", err)
	}

	login, err := readQuiet(conn)
	if err != nil {
		return "", fmt.Errorf("telnet read login: %w", err)
	}
	if strings.Contains(login, "Wrong password") {
		return "", fmt.Errorf("telnet: wrong password")
	}

	if cmd == "" {
		return login, nil
	}

	if _, err := conn.Write([]byte(cmd + "\n")); err != nil {
		return "", fmt.Errorf("telnet send command: %w", err)
	}
	out, err := readQuiet(conn)
	if err != nil {
		return "", fmt.Errorf("telnet read response: %w", err)
	}
	return out, nil
}

func (c *Client) Commands(cmds []string, timeout time.Duration) ([]string, error) {
	conn, err := net.DialTimeout("tcp", c.Addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("telnet dial: %w", err)
	}
	defer conn.Close()

	deadline := time.Now().Add(timeout)
	conn.SetDeadline(deadline)

	prompt, err := readQuiet(conn)
	if err != nil {
		return nil, fmt.Errorf("telnet read prompt: %w", err)
	}
	if prompt == "" {
		return nil, fmt.Errorf("telnet: empty response on connect — server is not responding")
	}
	if _, err := conn.Write([]byte(c.Pwd + "\n")); err != nil {
		return nil, fmt.Errorf("telnet send password: %w", err)
	}
	login, err := readQuiet(conn)
	if err != nil {
		return nil, fmt.Errorf("telnet read login: %w", err)
	}
	if strings.Contains(login, "Wrong password") {
		return nil, fmt.Errorf("telnet: wrong password")
	}

	out := make([]string, len(cmds))
	for i, cmd := range cmds {
		if _, err := conn.Write([]byte(cmd + "\n")); err != nil {
			return nil, fmt.Errorf("telnet send command %q: %w", cmd, err)
		}
		resp, err := readQuiet(conn)
		if err != nil {
			return nil, fmt.Errorf("telnet read response to %q: %w", cmd, err)
		}
		out[i] = resp
	}
	return out, nil
}

func readQuiet(conn net.Conn) (string, error) {
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		conn.SetReadDeadline(time.Now().Add(quietPeriod))
		n, err := conn.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				break
			}
			if sb.Len() > 0 {
				break
			}
			return sb.String(), err
		}
	}
	return sb.String(), nil
}
