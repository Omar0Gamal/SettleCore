package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/google/uuid"
	"github.com/settlecore/settlecore/services/internal/config"
	"github.com/settlecore/settlecore/services/internal/engine"
	"github.com/settlecore/settlecore/services/internal/evm"
	"github.com/settlecore/settlecore/services/internal/ledger"
	"github.com/settlecore/settlecore/services/internal/metrics"
	"github.com/settlecore/settlecore/services/internal/reconciler"
	"github.com/shopspring/decimal"
)

func setupTestGateway(t *testing.T) (*Server, ledger.Store, *metrics.Registry) {
	t.Helper()
	reg := metrics.NewRegistry()
	store := ledger.NewMemoryStore()
	eng, err := engine.NewBridge(engine.EngineConfig{DisableWAL: true}, reg)
	if err != nil {
		t.Fatalf("NewBridge: %v", err)
	}
	t.Cleanup(func() { _ = eng.Close() })

	cfg := config.Load()
	cfg.ChaosAPIEnabled = true
	srv := NewServer(Dependencies{
		Config:     cfg,
		Ledger:     store,
		Engine:     eng,
		SnapStore:  reconciler.NewMemorySnapshotStore(),
		MetricsReg: reg,
	})
	return srv, store, reg
}

func TestGatewayRESTChaosAndCancelOnDisconnect(t *testing.T) {
	ctx := context.Background()
	srv, store, reg := setupTestGateway(t)

	trader := ledger.DefaultTraderUserID
	mm := ledger.MarketMakerUserID
	_, _ = store.CreditDeposit(ctx, trader, ledger.AssetUSDC, decimal.NewFromInt(50000), "d1")
	_, _ = store.CreditDeposit(ctx, mm, ledger.AssetETH, decimal.NewFromInt(10), "d2")

	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	// 1. Healthz & CORS
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/healthz", nil)
	req.Header.Set("Origin", "http://localhost:3000")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz failed: resp=%v err=%v", resp, err)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "http://localhost:3000" {
		t.Fatalf("missing CORS header for http://localhost:3000")
	}
	_ = resp.Body.Close()

	// 2. Market Maker connects WebSocket with Cancel-on-Disconnect and places resting ASK
	wsURL := "ws" + strings.TrimPrefix(ts.URL, "http") + "/v1/ws"
	wsConn, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	subBytes, _ := json.Marshal(map[string]any{
		"op":                   "subscribe",
		"cancel_on_disconnect": true,
		"user_id":              mm.String(),
		"channels":             []string{"l2:ETH/USDC", "user:" + mm.String()},
	})
	if err := wsConn.Write(ctx, websocket.MessageText, subBytes); err != nil {
		t.Fatalf("ws subscribe: %v", err)
	}
	subDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(subDeadline) {
		if srv.WSHub().HasCancelOnDisconnectArmed(mm) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !srv.WSHub().HasCancelOnDisconnectArmed(mm) {
		t.Fatalf("expected Cancel-on-Disconnect subscription to be armed")
	}

	// MM places resting ASK 1.0 ETH @ 3100.00
	orderBody, _ := json.Marshal(map[string]string{
		"market_id":     "ETH/USDC",
		"side":          "SELL",
		"order_type":    "LIMIT",
		"time_in_force": "GTC",
		"price":         "3100.00",
		"quantity":      "1.00000000",
	})
	oReq, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/v1/orders", bytes.NewReader(orderBody))
	oReq.Header.Set("X-SettleCore-User-Id", mm.String())
	oReq.Header.Set("Idempotency-Key", uuid.NewString())
	oResp, err := http.DefaultClient.Do(oReq)
	if err != nil || oResp.StatusCode != http.StatusCreated {
		t.Fatalf("submit order failed: %v %v", oResp, err)
	}
	_ = oResp.Body.Close()

	// Close MM WebSocket -> Cancel-on-Disconnect must cancel resting ETH order and release 1.0 ETH!
	_ = wsConn.Close(websocket.StatusNormalClosure, "drop")
	deadline := time.Now().Add(2 * time.Second)
	codSettled := false
	var lastAvail, lastLocked decimal.Decimal
	for time.Now().Before(deadline) {
		if srv.WSHub().CancelOnDisconnectCount() >= 1 {
			mmBals, _ := store.ListUserBalances(ctx, mm)
			for _, b := range mmBals {
				if b.Asset == ledger.AssetETH {
					lastAvail = b.Available
					lastLocked = b.Locked
					if b.Available.Equal(decimal.NewFromInt(10)) && b.Locked.IsZero() {
						codSettled = true
					}
				}
			}
			if codSettled {
				break
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	if srv.WSHub().CancelOnDisconnectCount() < 1 {
		t.Fatalf("expected Cancel-on-Disconnect to trigger")
	}
	if !codSettled {
		t.Fatalf("expected 10 ETH released after COD, got avail=%s locked=%s", lastAvail, lastLocked)
	}

	// 3. Solvency proof endpoint
	pReq, _ := http.NewRequestWithContext(ctx, http.MethodGet, ts.URL+"/v1/solvency/proof", nil)
	pReq.Header.Set("X-SettleCore-User-Id", trader.String())
	pResp, err := http.DefaultClient.Do(pReq)
	if err != nil || pResp.StatusCode != http.StatusOK {
		t.Fatalf("solvency proof failed: %v %v", pResp, err)
	}
	var proof reconciler.SolvencyProofResponse
	_ = json.NewDecoder(pResp.Body).Decode(&proof)
	_ = pResp.Body.Close()
	if !proof.InvariantOK || proof.UserLeaf == nil || !reconciler.VerifyProof(proof.MerkleRoot, *proof.UserLeaf, proof.MerklePath) {
		t.Fatalf("invalid solvency proof response: %+v", proof)
	}

	// 4. All 4 chaos drills + reset
	postChaos := func(path string, payload string) {
		r, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+path, strings.NewReader(payload))
		r.Header.Set("Content-Type", "application/json")
		res, err := http.DefaultClient.Do(r)
		if err != nil || res.StatusCode != http.StatusOK {
			t.Fatalf("chaos %s failed: %v %v", path, res, err)
		}
		_ = res.Body.Close()
	}
	postChaos("/v1/chaos/reorg", `{"depth":3}`)
	postChaos("/v1/chaos/rpc-failover", `{"action":"kill"}`)
	postChaos("/v1/chaos/gas-spike", `{"base_fee_multiplier":10}`)
	postChaos("/v1/chaos/flash-crash", `{"pair":"ETH/USDC","drop_bps":1500}`)

	snap := reg.Snapshot()
	if snap.ReorgRollbacksTotal != 1 || snap.ActiveRPC != "rpc-proxy-2" || snap.StuckTxGasBumps != 1 || snap.CircuitBreakerStatus != "HALTED (>10% DEV)" {
		t.Fatalf("unexpected chaos telemetry snapshot: %+v", snap)
	}

	postChaos("/v1/chaos/reset", `{}`)
	snap = reg.Snapshot()
	if snap.ActiveRPC != "rpc-proxy-1" || snap.CircuitBreakerStatus != "NORMAL" {
		t.Fatalf("expected reset telemetry, got %+v", snap)
	}
}

func TestEIP712SignedOrdersAndLifecycleTrace(t *testing.T) {
	ctx := context.Background()
	srv, store, _ := setupTestGateway(t)

	trader := ledger.DefaultTraderUserID
	_, _ = store.CreditDeposit(ctx, trader, ledger.AssetUSDC, decimal.NewFromInt(20000), "eip712:dep")

	priv, err := evm.ParseHexPrivateKey("0x59c6995e998f97a5a0044966f0945389dc9e86dae88c7a8412f4603b6b78690d")
	if err != nil {
		t.Fatalf("ParseHexPrivateKey: %v", err)
	}
	signerAddr := crypto.PubkeyToAddress(priv.PublicKey).Hex()
	deadline := time.Now().Add(10 * time.Minute).Unix()
	nonce := uint64(1)

	payload := SignedOrderPayload{
		Market:   ledger.MarketETHUSDC,
		Side:     engine.SideCodeBuy,
		Price1e8: 3100 * ledger.CLOBScale,
		Qty1e8:   1 * ledger.CLOBScale,
		TIF:      engine.TIFCodeGTC,
		Nonce:    nonce,
		Deadline: deadline,
	}
	sig, err := srv.orderVerifier.SignOrder(priv, payload)
	if err != nil {
		t.Fatalf("SignOrder: %v", err)
	}

	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	sendOrder := func(body map[string]any) (*http.Response, []byte) {
		raw, _ := json.Marshal(body)
		req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/v1/orders", bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-SettleCore-User-Id", trader.String())
		req.Header.Set("Idempotency-Key", uuid.NewString())
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("POST /v1/orders: %v", err)
		}
		var out bytes.Buffer
		_, _ = out.ReadFrom(resp.Body)
		_ = resp.Body.Close()
		return resp, out.Bytes()
	}

	// 1. Valid signed order returns lifecycle trace with eip712_verified=true.
	resp, body := sendOrder(map[string]any{
		"market_id":      "ETH/USDC",
		"side":           "BUY",
		"order_type":     "LIMIT",
		"time_in_force":  "GTC",
		"price":          "3100.00",
		"quantity":       "1.00000000",
		"signature":      sig,
		"signer_address": signerAddr,
		"order_nonce":    nonce,
		"deadline":       deadline,
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("signed order failed: status=%d body=%s", resp.StatusCode, string(body))
	}
	var parsed SubmitOrderResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}
	if parsed.Lifecycle == nil ||
		!parsed.Lifecycle.EIP712Verified ||
		parsed.Lifecycle.ReservedAtNs <= 0 ||
		parsed.Lifecycle.WalAppendedAtNs < parsed.Lifecycle.ReservedAtNs ||
		parsed.Lifecycle.MatchedAtNs < parsed.Lifecycle.WalAppendedAtNs ||
		parsed.Lifecycle.WalSeq == 0 ||
		parsed.Lifecycle.JournalTxID == "" {
		t.Fatalf("unexpected lifecycle trace: %+v", parsed.Lifecycle)
	}

	// 2. Replayed nonce must be rejected.
	respReplay, _ := sendOrder(map[string]any{
		"market_id":      "ETH/USDC",
		"side":           "BUY",
		"order_type":     "LIMIT",
		"time_in_force":  "GTC",
		"price":          "3100.00",
		"quantity":       "1.00000000",
		"signature":      sig,
		"signer_address": signerAddr,
		"order_nonce":    nonce,
		"deadline":       deadline,
	})
	if respReplay.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on nonce replay, got %d", respReplay.StatusCode)
	}

	// 3. Wrong signer / tampered price must be rejected.
	respBadSig, _ := sendOrder(map[string]any{
		"market_id":      "ETH/USDC",
		"side":           "BUY",
		"order_type":     "LIMIT",
		"time_in_force":  "GTC",
		"price":          "3105.00",
		"quantity":       "1.00000000",
		"signature":      sig,
		"signer_address": signerAddr,
		"order_nonce":    nonce + 1,
		"deadline":       deadline,
	})
	if respBadSig.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on tampered signature, got %d", respBadSig.StatusCode)
	}

	// 4. Expired deadline must be rejected.
	expiredDeadline := time.Now().Add(-10 * time.Second).Unix()
	expiredSig, _ := srv.orderVerifier.SignOrder(priv, SignedOrderPayload{
		Market:   ledger.MarketETHUSDC,
		Side:     engine.SideCodeBuy,
		Price1e8: 3100 * ledger.CLOBScale,
		Qty1e8:   1 * ledger.CLOBScale,
		TIF:      engine.TIFCodeGTC,
		Nonce:    nonce + 2,
		Deadline: expiredDeadline,
	})
	respExp, _ := sendOrder(map[string]any{
		"market_id":      "ETH/USDC",
		"side":           "BUY",
		"order_type":     "LIMIT",
		"time_in_force":  "GTC",
		"price":          "3100.00",
		"quantity":       "1.00000000",
		"signature":      expiredSig,
		"signer_address": signerAddr,
		"order_nonce":    nonce + 2,
		"deadline":       expiredDeadline,
	})
	if respExp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401 on expired order, got %d", respExp.StatusCode)
	}
}

func TestChaosGatewayCrashRecovery(t *testing.T) {
	ctx := context.Background()
	srv, store, _ := setupTestGateway(t)
	trader := ledger.DefaultTraderUserID
	_, _ = store.CreditDeposit(ctx, trader, ledger.AssetUSDC, decimal.NewFromInt(10000), "crash:dep")

	ts := httptest.NewServer(srv.Router())
	defer ts.Close()

	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/v1/chaos/gateway-crash", strings.NewReader(`{}`))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("POST /v1/chaos/gateway-crash failed: resp=%v err=%v", resp, err)
	}
	var out struct {
		Status              string `json:"status"`
		OrphansDetected     int    `json:"orphans_detected"`
		OrphansRolledBack   int    `json:"orphans_rolled_back"`
		ReservesInvariantOK bool   `json:"reserves_invariant_ok"`
		AbortedOrderID      string `json:"aborted_order_id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode gateway-crash: %v", err)
	}
	_ = resp.Body.Close()

	if out.Status != "recovered_orphaned_reservations" || out.OrphansDetected != 1 || out.OrphansRolledBack != 1 || !out.ReservesInvariantOK {
		t.Fatalf("unexpected gateway-crash output: %+v", out)
	}

	bals, _ := store.ListUserBalances(ctx, trader)
	for _, b := range bals {
		if b.Asset == ledger.AssetUSDC {
			if !b.Available.Equal(decimal.NewFromInt(10000)) || !b.Locked.IsZero() {
				t.Fatalf("expected 10000 available / 0 locked after orphan rollback, got avail=%s locked=%s", b.Available, b.Locked)
			}
		}
	}
}
