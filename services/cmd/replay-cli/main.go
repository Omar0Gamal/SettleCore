package main

import (
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"

	"github.com/settlecore/settlecore/services/internal/config"
	"github.com/settlecore/settlecore/services/internal/engine"
)

func main() {
	cfg := config.Load()
	walPath := flag.String("wal", cfg.EngineWALDir+"/engine.wal", "Path to 64-byte binary WAL file")
	snapDir := flag.String("snapshot-dir", cfg.EngineSnapshotDir, "Path to snapshot directory")
	asJSON := flag.Bool("json", false, "Output result as JSON")
	flag.Parse()

	b, replayed, err := engine.ReplayFromWAL(*walPath, *snapDir, engine.EngineConfig{
		MaxRestingOrders:  cfg.EngineMaxRestingOrders,
		CircuitBreakerBps: cfg.EngineCircuitBreakerBps,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "replay error: %v\n", err)
		os.Exit(1)
	}
	defer func() { _ = b.Close() }()

	digest := b.StateHash()
	hexDigest := hex.EncodeToString(digest[:])

	if *asJSON {
		out, _ := json.MarshalIndent(map[string]any{
			"wal_path":         *walPath,
			"replayed_records": replayed,
			"state_sha256":     hexDigest,
		}, "", "  ")
		fmt.Println(string(out))
		return
	}

	fmt.Printf("SettleCore Replay Complete | records=%d | state_sha256=%s\n", replayed, hexDigest)
}
