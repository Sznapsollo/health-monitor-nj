package api

import (
	"context"
	"errors"
	"testing"
)

func stalledSender(queue int) *wsSender {
	ctx, cancel := context.WithCancel(context.Background())
	return &wsSender{
		ctx:   ctx,
		stop:  cancel,
		queue: make(chan []byte, queue),
		done:  make(chan struct{}),
	}
}

func TestSendNeverBlocksOnAStalledViewer(t *testing.T) {
	s := stalledSender(2)
	for i := 0; i < 2; i++ {
		if err := s.enqueue([]byte("x")); err != nil {
			t.Fatalf("message %d: %v, want it queued", i, err)
		}
	}
	done := make(chan error, 1)
	go func() { done <- s.enqueue([]byte("x")) }()
	select {
	case err := <-done:
		if !errors.Is(err, errViewerBehind) {
			t.Fatalf("err = %v, want %v", err, errViewerBehind)
		}
	case <-t.Context().Done():
		t.Fatal("enqueue blocked on a full queue")
	}
}

func TestSendAfterTheSocketIsGone(t *testing.T) {
	s := stalledSender(2)
	close(s.done)
	if err := s.enqueue([]byte("x")); !errors.Is(err, errViewerGone) {
		t.Fatalf("err = %v, want %v", err, errViewerGone)
	}
}
