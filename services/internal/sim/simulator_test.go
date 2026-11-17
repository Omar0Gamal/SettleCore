package sim

import (
	"flag"
	"strconv"
	"strings"
	"testing"
)

var simSeedFlag = flag.String("seed", "", "optional hex or decimal seed to replay a single simulation run")

func TestDeterministicSimulation(t *testing.T) {
	if *simSeedFlag != "" {
		raw := strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(*simSeedFlag), "0x"), "0X")
		base := 10
		if strings.HasPrefix(strings.TrimSpace(*simSeedFlag), "0x") || strings.HasPrefix(strings.TrimSpace(*simSeedFlag), "0X") {
			base = 16
		}
		seed, err := strconv.ParseUint(raw, base, 64)
		if err != nil {
			t.Fatalf("invalid -seed %q: %v", *simSeedFlag, err)
		}
		rep, err := RunSeed(seed, 200)
		if err != nil {
			t.Fatalf("RunSeed failed: %v", err)
		}
		t.Logf("seed=0x%x steps=%d hash=%x", rep.Seed, rep.StepsExecuted, rep.FinalStateHash)
		return
	}

	const numSeeds = 100
	const stepsPerSeed = 200

	var totalSteps int
	var totalOrders int
	var totalReorgs int
	var totalCrashes int

	for i := 0; i < numSeeds; i++ {
		seed := uint64(0x5E771E0000 + i + 1)
		rep, err := RunSeed(seed, stepsPerSeed)
		if err != nil {
			t.Fatalf("deterministic simulation failed: %v", err)
		}
		if rep.StepsExecuted != stepsPerSeed {
			t.Fatalf("seed=0x%x executed %d steps, want %d", seed, rep.StepsExecuted, stepsPerSeed)
		}
		totalSteps += rep.StepsExecuted
		totalOrders += rep.OrdersSubmitted
		totalReorgs += rep.ReorgsInjected
		totalCrashes += rep.GatewayCrashesInjected
	}

	// Verify same seed produces bit-identical FinalStateHash across independent runs.
	r1, err := RunSeed(0x5E771E0001, stepsPerSeed)
	if err != nil {
		t.Fatalf("RunSeed repeat 1: %v", err)
	}
	r2, err := RunSeed(0x5E771E0001, stepsPerSeed)
	if err != nil {
		t.Fatalf("RunSeed repeat 2: %v", err)
	}
	if r1.FinalStateHash != r2.FinalStateHash {
		t.Fatalf("non-deterministic final StateHash for identical seed: %x != %x", r1.FinalStateHash, r2.FinalStateHash)
	}

	t.Logf("100 seeds x 200 steps = %d fault-injected transitions passed | orders=%d reorgs=%d gateway_crashes=%d",
		totalSteps, totalOrders, totalReorgs, totalCrashes)
}

func TestSimulatorCatchesInjectedBug(t *testing.T) {
	// 1. Unbacked deposit bug must be caught immediately by I-2 Vault Solvency.
	_, err := runSeedWithFault(0xBEEF01, 25, simFaultConfig{InjectBug: "unbalanced_balance"})
	if err == nil {
		t.Fatalf("expected simulator to catch unbalanced_balance bug")
	}
	if !strings.Contains(err.Error(), "invariant=I-2") || !strings.Contains(err.Error(), "go test ./internal/sim -run TestDeterministicSimulation -seed=0xbeef01") {
		t.Fatalf("unexpected error format for unbalanced_balance: %v", err)
	}

	// 2. Skipped reorg reversal bug must be caught by I-2 Vault Solvency.
	_, err = runSeedWithFault(0xBEEF02, 200, simFaultConfig{InjectBug: "skip_reorg_reversal"})
	if err == nil {
		t.Fatalf("expected simulator to catch skip_reorg_reversal bug")
	}
	if !strings.Contains(err.Error(), "invariant=I-2") || !strings.Contains(err.Error(), "-seed=0xbeef02") {
		t.Fatalf("unexpected error format for skip_reorg_reversal: %v", err)
	}

	// 3. Divergent WAL replay state must be caught immediately by I-6.
	_, err = runSeedWithFault(0xBEEF03, 25, simFaultConfig{InjectBug: "corrupt_wal_replay"})
	if err == nil {
		t.Fatalf("expected simulator to catch corrupt_wal_replay bug")
	}
	if !strings.Contains(err.Error(), "invariant=I-6") || !strings.Contains(err.Error(), "-seed=0xbeef03") {
		t.Fatalf("unexpected error format for corrupt_wal_replay: %v", err)
	}
}
