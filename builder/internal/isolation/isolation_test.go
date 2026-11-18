package isolation

import (
	"errors"
	"net"
	"testing"
	"time"
)

const testTimeout = 1500 * time.Millisecond

func listen(t *testing.T) net.Listener {
	t.Helper()
	var config net.ListenConfig
	listener, err := config.Listen(t.Context(), "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	return listener
}

func TestWaitFailsWhileTheAddressAccepts(t *testing.T) {
	listener := listen(t)
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, acceptErr := listener.Accept()
			if acceptErr != nil {
				return
			}
			if closeErr := conn.Close(); closeErr != nil {
				t.Error(closeErr)
			}
		}
	}()
	t.Cleanup(func() {
		if closeErr := listener.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		<-done
	})
	err := Wait(t.Context(), listener.Addr().String(), testTimeout)
	if !errors.Is(err, errNotIsolated) {
		t.Fatalf("Wait = %v, want %v", err, errNotIsolated)
	}
}

func TestWaitFailsWhileTheAddressRefuses(t *testing.T) {
	listener := listen(t)
	address := listener.Addr().String()
	if closeErr := listener.Close(); closeErr != nil {
		t.Fatal(closeErr)
	}
	err := Wait(t.Context(), address, testTimeout)
	if !errors.Is(err, errNotIsolated) {
		t.Fatalf("Wait = %v, want %v", err, errNotIsolated)
	}
}

func TestWaitReturnsOnceDialsAreFiltered(t *testing.T) {
	// 192.0.2.0/24 is reserved for documentation: dials to it time out or find no route.
	if err := Wait(t.Context(), "192.0.2.1:10250", time.Minute); err != nil {
		t.Fatalf("Wait = %v, want nil", err)
	}
}
