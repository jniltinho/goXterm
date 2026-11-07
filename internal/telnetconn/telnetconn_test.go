package telnetconn

import (
	"net"
	"strconv"
	"testing"
	"time"
)

// TestTelnetNegotiation runs a fake telnet server that sends IAC WILL ECHO then "hello", and checks
// the client answers IAC DO ECHO (accepts server echo) and surfaces "hello" with the IAC stripped.
func TestTelnetNegotiation(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	srvReply := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.Write([]byte{iac, will, optEcho}) // offer to echo
		c.Write([]byte("hello"))            // payload
		buf := make([]byte, 8)
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _ := c.Read(buf) // expect the client's IAC DO ECHO
		srvReply <- append([]byte(nil), buf[:n]...)
	}()

	_, portStr, _ := net.SplitHostPort(ln.Addr().String())
	port, _ := strconv.Atoi(portStr)
	got := make(chan []byte, 4)
	s, err := Connect(Config{Host: "127.0.0.1", Port: port}, func(b []byte) {
		got <- append([]byte(nil), b...)
	}, func(error) {})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	select {
	case b := <-got:
		if string(b) != "hello" {
			t.Fatalf("payload = %q, want %q", b, "hello")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no payload (IAC not stripped or not forwarded)")
	}
	select {
	case r := <-srvReply:
		if len(r) != 3 || r[0] != iac || r[1] != do || r[2] != optEcho {
			t.Fatalf("client reply = % x, want IAC DO ECHO (% x)", r, []byte{iac, do, optEcho})
		}
	case <-time.After(2 * time.Second):
		t.Fatal("client did not answer the ECHO negotiation")
	}
}
