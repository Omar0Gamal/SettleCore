package engine

import (
	"math/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/metrics"
)

func TestEventLogBinaryRoundTripAndTornTail(t *testing.T) {
	tmp := t.TempDir()
	evPath := filepath.Join(tmp, "engine.wal.evlog")

	want := []WalEventRecord{
		{
			EventType:    EventTypeOrderAccepted,
			MarketID:     MarketCodeETHUSDC,
			Status:       ExecStatusAccepted,
			Flags:        0,
			WalSeq:       1,
			OrderID:      1001,
			ExecPrice1e8: 3100 * ledger.CLOBScale,
			FillQty1e8:   2 * ledger.CLOBScale,
		},
		{
			EventType:    EventTypeTradeFill,
			MarketID:     MarketCodeETHUSDC,
			Status:       ExecStatusFilled,
			Flags:        EventFlagSideSell | EventFlagMakerTerminal | EventFlagTakerTerminal,
			WalSeq:       2,
			OrderID:      1002,
			MakerOrderID: 1001,
			TradeID:      1,
			ExecPrice1e8: 3100 * ledger.CLOBScale,
			FillQty1e8:   2 * ledger.CLOBScale,
		},
		{
			EventType: EventTypeOrderCanceled,
			MarketID:  MarketCodeBTCUSDC,
			Status:    ExecStatusCanceled,
			WalSeq:    3,
			OrderID:   1003,
		},
	}

	if err := WriteEventLog(evPath, want); err != nil {
		t.Fatalf("WriteEventLog: %v", err)
	}

	got, torn, err := ReadEventLog(evPath)
	if err != nil || torn || len(got) != len(want) {
		t.Fatalf("ReadEventLog: len=%d torn=%v err=%v", len(got), torn, err)
	}
	for i := range want {
		if got[i].Magic != EventLogMagic ||
			got[i].Version != EventLogVersion ||
			got[i].EventType != want[i].EventType ||
			got[i].MarketID != want[i].MarketID ||
			got[i].Status != want[i].Status ||
			got[i].Flags != want[i].Flags ||
			got[i].WalSeq != want[i].WalSeq ||
			got[i].OrderID != want[i].OrderID ||
			got[i].MakerOrderID != want[i].MakerOrderID ||
			got[i].TradeID != want[i].TradeID ||
			got[i].ExecPrice1e8 != want[i].ExecPrice1e8 ||
			got[i].FillQty1e8 != want[i].FillQty1e8 {
			t.Fatalf("event[%d] mismatch: got=%+v want=%+v", i, got[i], want[i])
		}
	}

	// Simulate a partial 19-byte torn tail write and confirm safe truncation.
	f, err := os.OpenFile(evPath, os.O_WRONLY|os.O_APPEND, 0o640)
	if err != nil {
		t.Fatalf("OpenFile: %v", err)
	}
	_, _ = f.Write(make([]byte, 19))
	_ = f.Close()

	got2, torn2, err := ReadEventLog(evPath)
	if err != nil || !torn2 || len(got2) != len(want) {
		t.Fatalf("expected torn tail truncation to %d records, got len=%d torn=%v err=%v", len(want), len(got2), torn2, err)
	}
}

func TestDifferentialEngineEquivalenceAndWALReplay(t *testing.T) {
	const totalCommands = 25_000
	const checkpointInterval = 5_000

	tmpA := t.TempDir()
	tmpB := t.TempDir()

	cfgA := EngineConfig{
		WALDir:            tmpA,
		SnapshotDir:       filepath.Join(tmpA, "snap"),
		WALFsyncMode:      "buffered",
		MaxRestingOrders:  100_000,
		CircuitBreakerBps: 1000,
	}
	cfgB := EngineConfig{
		WALDir:            tmpB,
		SnapshotDir:       filepath.Join(tmpB, "snap"),
		WALFsyncMode:      "buffered",
		MaxRestingOrders:  100_000,
		CircuitBreakerBps: 1000,
	}

	engA, err := NewBridge(cfgA, metrics.NewRegistry())
	if err != nil {
		t.Fatalf("NewBridge A: %v", err)
	}
	defer func() { _ = engA.Close() }()

	engB, err := NewBridge(cfgB, metrics.NewRegistry())
	if err != nil {
		t.Fatalf("NewBridge B: %v", err)
	}
	defer func() { _ = engB.Close() }()

	rng := rand.New(rand.NewSource(0x5E771EC0DE)) // #nosec G404
	accounts := make([][2]uint64, 16)
	for i := range accounts {
		lo, hi := UUIDToUint64Pair(uuid.NewSHA1(uuid.NameSpaceOID, []byte{byte(i + 1)}))
		accounts[i] = [2]uint64{lo, hi}
	}

	liveOrders := make([]uint64, 0, 4096)
	liveMarkets := make(map[uint64]uint16, 4096)
	nextOrderID := uint64(1)
	markETH := int64(3100) * ledger.CLOBScale
	markBTC := int64(95000) * ledger.CLOBScale

	for step := 1; step <= totalCommands; step++ {
		var cmd OrderCmd
		mktCode := MarketCodeETHUSDC
		mark := markETH
		if rng.Intn(4) == 0 {
			mktCode = MarketCodeBTCUSDC
			mark = markBTC
		}
		acc := accounts[rng.Intn(len(accounts))]

		roll := rng.Intn(100)
		switch {
		case roll < 5 && step%25 == 0:
			// Mark price collar update (+/- 2% around baseline)
			deltaPct := int64(rng.Intn(5) - 2)
			newMark := mark + (mark*deltaPct)/100
			// Snap to 100-tick USDC step
			newMark = (newMark / ledger.SixDecimalLotStep) * ledger.SixDecimalLotStep
			if mktCode == MarketCodeETHUSDC {
				markETH = newMark
			} else {
				markBTC = newMark
			}
			cmd = OrderCmd{
				MarketID: mktCode,
				Opcode:   OpUpdateMarkPrice,
				Price1e8: newMark,
			}
		case roll < 18 && len(liveOrders) > 0:
			// Cancel an existing order (or occasional already-filled order)
			idx := rng.Intn(len(liveOrders))
			oid := liveOrders[idx]
			cmd = OrderCmd{
				OrderID:  oid,
				MarketID: liveMarkets[oid],
				Opcode:   OpCancelOrder,
			}
		case roll == 19:
			// Cancel-all for a random account
			cmd = OrderCmd{
				AccountIDLow:  acc[0],
				AccountIDHigh: acc[1],
				MarketID:      mktCode,
				Opcode:        OpCancelAllAccount,
			}
		default:
			// Mixed LIMIT / MARKET with GTC / IOC / FOK and all 3 STP modes
			side := uint8(SideCodeBuy)
			if rng.Intn(2) == 1 {
				side = SideCodeSell
			}
			ot := uint8(OrderTypeCodeLimit)
			tif := uint8(TIFCodeGTC)
			rTIF := rng.Intn(10)
			switch {
			case rTIF < 6:
				tif = TIFCodeGTC
			case rTIF < 8:
				tif = TIFCodeIOC
			default:
				tif = TIFCodeFOK
			}
			if rng.Intn(10) == 0 {
				ot = OrderTypeCodeMarket
				if tif == TIFCodeGTC {
					tif = TIFCodeIOC
				}
			}

			// Keep price within +/- 6% of mark most of the time, with occasional +/- 12% CB probe.
			bpsOffset := int64(rng.Intn(1201) - 600)
			if rng.Intn(250) == 0 {
				bpsOffset = -1200
			}
			px := mark * (10_000 + bpsOffset) / 10_000
			px = (px / ledger.SixDecimalLotStep) * ledger.SixDecimalLotStep
			if px < ledger.SixDecimalLotStep {
				px = ledger.SixDecimalLotStep
			}
			qty := int64(rng.Intn(500)+1) * (ledger.CLOBScale / 100) // 0.01 .. 5.00 units
			stp := uint8(rng.Intn(3))

			oid := nextOrderID
			nextOrderID++
			if ot == OrderTypeCodeLimit && tif == TIFCodeGTC {
				liveOrders = append(liveOrders, oid)
				liveMarkets[oid] = mktCode
				if len(liveOrders) > 4000 {
					liveOrders = liveOrders[1:]
				}
			}
			cmd = OrderCmd{
				OrderID:       oid,
				AccountIDLow:  acc[0],
				AccountIDHigh: acc[1],
				Price1e8:      px,
				Qty1e8:        qty,
				TimestampNs:   uint64(step) * 1000,
				MarketID:      mktCode,
				Opcode:        OpSubmitOrder,
				Side:          side,
				OrderType:     ot,
				TimeInForce:   tif,
				STPMode:       stp,
			}
		}

		repA, errA := engA.Submit(cmd)
		repB, errB := engB.Submit(cmd)
		if (errA != nil) != (errB != nil) || (errA != nil && errA.Error() != errB.Error()) {
			t.Fatalf("step %d error mismatch: A=%v B=%v cmd=%+v", step, errA, errB, cmd)
		}
		assertExecReportsEqual(t, step, repA, repB)

		// Periodically reset circuit breaker after probe so the remaining workload continues matching.
		if repA.ErrorCode == ErrCodeCircuitBreakerTripped {
			mktStr, _ := CodeToMarketID(cmd.MarketID)
			markToSet := markETH
			if cmd.MarketID == MarketCodeBTCUSDC {
				markToSet = markBTC
			}
			_, errRA := engA.Submit(OrderCmd{MarketID: cmd.MarketID, Opcode: OpUpdateMarkPrice, Price1e8: markToSet})
			_, errRB := engB.Submit(OrderCmd{MarketID: cmd.MarketID, Opcode: OpUpdateMarkPrice, Price1e8: markToSet})
			if errRA != nil || errRB != nil {
				t.Fatalf("reset CB %s failed: %v / %v", mktStr, errRA, errRB)
			}
		}

		if step%1000 == 0 {
			assertEngineStatesMatch(t, step, engA, engB)
		}

		if step%checkpointInterval == 0 {
			if err := engA.SyncWAL(); err != nil {
				t.Fatalf("step %d SyncWAL A: %v", step, err)
			}
			if err := engB.SyncWAL(); err != nil {
				t.Fatalf("step %d SyncWAL B: %v", step, err)
			}

			walPathA := filepath.Join(tmpA, "engine.wal")
			replayed, replayedCount, err := ReplayFromWAL(walPathA, "", EngineConfig{
				MaxRestingOrders:  100_000,
				CircuitBreakerBps: 1000,
			})
			if err != nil {
				t.Fatalf("step %d ReplayFromWAL: %v", step, err)
			}
			if replayedCount == 0 {
				_ = replayed.Close()
				t.Fatalf("step %d ReplayFromWAL replayed 0 records", step)
			}
			if replayed.StateHash() != engA.StateHash() {
				_ = replayed.Close()
				t.Fatalf("step %d cold WAL replay SHA-256 mismatch: got %x want %x", step, replayed.StateHash(), engA.StateHash())
			}
			assertEngineStatesMatch(t, step, engA, replayed)
			_ = replayed.Close()

			// Verify both engines emitted identical 64-byte "SCEV" execution event logs.
			evA, tornA, errA := ReadEventLog(walPathA + ".evlog")
			evB, tornB, errB := ReadEventLog(filepath.Join(tmpB, "engine.wal.evlog"))
			if errA != nil || errB != nil || tornA || tornB {
				t.Fatalf("step %d ReadEventLog failed: errA=%v errB=%v tornA=%v tornB=%v", step, errA, errB, tornA, tornB)
			}
			if len(evA) != len(evB) {
				t.Fatalf("step %d event log count mismatch: A=%d B=%d", step, len(evA), len(evB))
			}
			for i := range evA {
				if evA[i] != evB[i] {
					t.Fatalf("step %d event[%d] mismatch: A=%+v B=%+v", step, i, evA[i], evB[i])
				}
			}
		}
	}
}

func assertExecReportsEqual(t *testing.T, step int, a, b ExecReport) {
	t.Helper()
	if a.WalSeq != b.WalSeq ||
		a.OrderID != b.OrderID ||
		a.FilledQty1e8 != b.FilledQty1e8 ||
		a.RemainingQty1e8 != b.RemainingQty1e8 ||
		a.Status != b.Status ||
		a.ErrorCode != b.ErrorCode ||
		len(a.Fills) != len(b.Fills) ||
		len(a.CanceledIDs) != len(b.CanceledIDs) {
		t.Fatalf("step %d ExecReport mismatch:\nA=%+v\nB=%+v", step, a, b)
	}
	for i := range a.Fills {
		if a.Fills[i] != b.Fills[i] {
			t.Fatalf("step %d Fill[%d] mismatch: A=%+v B=%+v", step, i, a.Fills[i], b.Fills[i])
		}
	}
	for i := range a.CanceledIDs {
		if a.CanceledIDs[i] != b.CanceledIDs[i] {
			t.Fatalf("step %d CanceledID[%d] mismatch: A=%d B=%d", step, i, a.CanceledIDs[i], b.CanceledIDs[i])
		}
	}
}

func assertEngineStatesMatch(t *testing.T, step int, a, b *Bridge) {
	t.Helper()
	hashA := a.StateHash()
	hashB := b.StateHash()
	if hashA != hashB {
		t.Fatalf("step %d StateHash mismatch: A=%x B=%x", step, hashA, hashB)
	}
	for _, mkt := range []string{ledger.MarketETHUSDC, ledger.MarketBTCUSDC} {
		dA, errA := a.Depth(mkt, 50)
		dB, errB := b.Depth(mkt, 50)
		if errA != nil || errB != nil {
			t.Fatalf("step %d Depth(%s) err: %v / %v", step, mkt, errA, errB)
		}
		if dA.MarkPrice1e8 != dB.MarkPrice1e8 ||
			dA.CircuitBreakerTripped != dB.CircuitBreakerTripped ||
			len(dA.Bids) != len(dB.Bids) ||
			len(dA.Asks) != len(dB.Asks) {
			t.Fatalf("step %d Depth(%s) header mismatch: A=%+v B=%+v", step, mkt, dA, dB)
		}
		for i := range dA.Bids {
			if dA.Bids[i] != dB.Bids[i] {
				t.Fatalf("step %d Depth(%s) bid[%d] mismatch: A=%+v B=%+v", step, mkt, i, dA.Bids[i], dB.Bids[i])
			}
		}
		for i := range dA.Asks {
			if dA.Asks[i] != dB.Asks[i] {
				t.Fatalf("step %d Depth(%s) ask[%d] mismatch: A=%+v B=%+v", step, mkt, i, dA.Asks[i], dB.Asks[i])
			}
		}
	}
}
