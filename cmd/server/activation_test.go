package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDeploymentActivationRequiresMatchingMarker(t *testing.T) {
	file := filepath.Join(t.TempDir(), "active")
	if err := os.WriteFile(file, []byte("blue\n"), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	started := make(chan struct{}, 1)
	go waitForActivation(ctx, "green", file, func(context.Context) { started <- struct{}{} })
	select {
	case <-started:
		t.Fatal("standby started writers")
	case <-time.After(20 * time.Millisecond):
	}
	if err := os.WriteFile(file, []byte("green\n"), 0600); err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("active slot did not start writers")
	}
}
func TestDeploymentActivationCancelledStandby(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	waitForActivation(ctx, "green", filepath.Join(t.TempDir(), "missing"), func(context.Context) { t.Fatal("cancelled standby activated") })
}
