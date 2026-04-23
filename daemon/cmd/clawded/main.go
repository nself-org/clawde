// Package main is the entry point for clawded — the ClawDE background daemon.
//
// This binary manages the process pool, JSON-RPC 2.0 server, and session state.
// It replaces the legacy Rust daemon (apps/daemon/) per the Go migration (SP-21.T09).
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/nself-org/clawde/daemon/internal/services/processpool"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cliBinary := os.Getenv("CLAWDE_CLI_BINARY")
	if cliBinary == "" {
		cliBinary = "claude"
	}

	pool := processpool.New(processpool.Config{
		TargetSize: 2,
		CliBinary:  cliBinary,
	})

	go pool.Replenish(ctx)

	fmt.Println("clawded: process pool initialized, awaiting connections")

	<-ctx.Done()

	fmt.Println("clawded: shutting down")
	pool.Shutdown()
}
