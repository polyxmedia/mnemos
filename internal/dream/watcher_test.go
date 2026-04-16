package dream_test

import (
	"context"
	"testing"
	"time"
)

func TestWatchStopsOnContextCancel(t *testing.T) {
	ds, _, _ := newDream(t)
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- ds.Watch(ctx, 10*time.Millisecond) }()

	time.Sleep(30 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("watch returned error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("watch did not stop after cancel")
	}
}

func TestWatchZeroIntervalReturnsImmediately(t *testing.T) {
	ds, _, _ := newDream(t)
	err := ds.Watch(context.Background(), 0)
	if err != nil {
		t.Errorf("zero interval should no-op, got err %v", err)
	}
}
