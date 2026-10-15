package main

import (
	"bufio"
	"fmt"
	"net"
	"testing"
	"time"
)

func TestEchoExchangesNonHTTPBytes(t *testing.T) {
	server, caller := net.Pipe()
	t.Cleanup(func() {
		if err := caller.Close(); err != nil {
			t.Errorf("close caller: %v", err)
		}
	})
	if err := caller.SetDeadline(time.Now().Add(connectionTimeout)); err != nil {
		t.Fatal(err)
	}
	go echo(server)
	const payload = "private-tcp-ok"
	if _, err := fmt.Fprintln(caller, payload); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(caller).ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if line != payload+"\n" {
		t.Fatalf("response = %q, want %q", line, payload+"\n")
	}
}
