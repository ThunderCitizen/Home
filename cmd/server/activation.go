package main

import (
	"context"
	"os"
	"strings"
	"time"
)

// HTTP candidates are read-only until the deployer has stopped the previous
// writer and atomically selected this slot. Never activate based on elapsed time.
func waitForActivation(ctx context.Context, slot, file string, start func(context.Context)) {
	if slot == "" {
		start(ctx)
		return
	}
	if file == "" {
		log.Error("deployment slot has no activation file; writers remain disabled")
		return
	}
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	for {
		if ctx.Err() != nil {
			return
		}
		contents, err := os.ReadFile(file)
		if err == nil && strings.TrimSpace(string(contents)) == slot {
			log.Info("deployment slot activated", "slot", slot)
			start(ctx)
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
		}
	}
}
