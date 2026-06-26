// clawde-proxy — lightweight SQLite-backed proxy daemon for ClawDE.
// Purpose: Manages chat session storage, routing table, and request logging for the
//   ClawDE AI dev environment. Runs as a background daemon bound to 127.0.0.1:3780.
// Inputs:  CLAWDE_* and NSELF_* env vars; --db-path flag override.
// Outputs: SQLite DB migrated and ready; HTTP server on :3780; PID file at DataDir/proxy.pid.
// Constraints: Single-writer SQLite (WAL mode). Graceful shutdown on SIGTERM/SIGINT (5s deadline).
//   No pool logic or lane routing in this scaffold; all non-health routes return 501.
// SPORT: F08-SERVICE-INVENTORY.md clawde-proxy row (port 3780)
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/nself-org/clawde-proxy/config"
	"github.com/nself-org/clawde-proxy/db"
	"github.com/nself-org/clawde-proxy/server"
)

func main() {
	dbPathFlag := flag.String("db-path", "", "Override SQLite DB path (default: CLAWDE_DATA_DIR/clawde.db)")
	flag.Parse()

	cfg := config.New()
	if *dbPathFlag != "" {
		cfg.DBPath = *dbPathFlag
	}

	// Ensure DataDir exists.
	if err := os.MkdirAll(cfg.DataDir, 0o700); err != nil {
		log.Fatalf("clawde-proxy: mkdir %s: %v", cfg.DataDir, err)
	}

	// Open database and run migrations.
	database, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatalf("clawde-proxy: open db: %v", err)
	}
	defer database.Close()

	if err := db.Migrate(database); err != nil {
		log.Fatalf("clawde-proxy: migrations: %v", err)
	}
	fmt.Println("clawde-proxy: migrations complete")

	// Write PID file.
	pidPath := filepath.Join(cfg.DataDir, "proxy.pid")
	if err := writePID(pidPath); err != nil {
		log.Fatalf("clawde-proxy: write pid: %v", err)
	}
	defer os.Remove(pidPath)

	// Start HTTP server.
	addr := cfg.ProxyBind + ":" + cfg.ProxyPort
	srv := server.New(addr)
	go func() {
		log.Printf("clawde-proxy: listening on %s", addr)
		if err := srv.Start(); err != nil {
			log.Printf("clawde-proxy: server stopped: %v", err)
		}
	}()

	// Block until SIGTERM or SIGINT.
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGTERM, syscall.SIGINT)
	<-quit

	log.Println("clawde-proxy: shutting down...")
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(cfg.ShutdownTimeoutS)*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Printf("clawde-proxy: shutdown error: %v", err)
	}
	// PID file removed by deferred os.Remove above.
	log.Println("clawde-proxy: stopped")
}

// writePID writes the current process PID to path.
func writePID(path string) error {
	return os.WriteFile(path, []byte(strconv.Itoa(os.Getpid())+"\n"), 0o644)
}
