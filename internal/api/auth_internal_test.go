package api

import (
	"fmt"
	"testing"
	"time"
)

func TestLoginLimiterStaysBounded(t *testing.T) {
	clock := time.Date(2026, 10, 3, 10, 0, 0, 0, time.UTC)
	l := newLoginLimiter(func() time.Time { return clock })
	for i := 0; i < loginClients+100; i++ {
		l.failed(fmt.Sprintf("10.0.%d.%d", i/256, i%256))
		clock = clock.Add(time.Millisecond)
	}
	if got := len(l.failures); got > loginClients {
		t.Fatalf("limiter holds %d clients, want at most %d", got, loginClients)
	}
	if _, kept := l.failures["10.0.0.0"]; kept {
		t.Error("the oldest client was kept over newer ones")
	}

	clock = clock.Add(loginWindow + time.Second)
	l.failed("192.0.2.1")
	if got := len(l.failures); got != 1 {
		t.Errorf("limiter holds %d clients after the window, want only the new one", got)
	}
}
