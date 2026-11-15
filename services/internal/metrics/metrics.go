package metrics

import (
	"net/http"
	"slices"
	"sync"
	"sync/atomic"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Registry holds all SettleCore Prometheus metrics and lightweight atomic snapshots for telemetry streaming.
type Registry struct {
	promReg *prometheus.Registry

	OrderMatchLatencyUs   prometheus.Histogram
	IndexerBlockLag       prometheus.Gauge
	ReorgRollbacksTotal   prometheus.Counter
	StuckTxGasBumps       prometheus.Counter
	ReservesInvariantOK   prometheus.Gauge
	ActiveRPCEndpoint     *prometheus.GaugeVec
	CircuitBreakerTripped *prometheus.GaugeVec

	mu            sync.Mutex
	latencyWindow []int64
	activeRPCName string
	reorgCount    atomic.Int64
	gasBumpCount  atomic.Int64
	blockLag      atomic.Int64
	invariantOK   atomic.Bool
	cbTripped     atomic.Bool
}

// NewRegistry creates and registers all SettleCore metrics on a dedicated Prometheus registry.
func NewRegistry() *Registry {
	reg := prometheus.NewRegistry()
	r := &Registry{
		promReg: reg,
		OrderMatchLatencyUs: prometheus.NewHistogram(prometheus.HistogramOpts{
			Name:    "order_match_latency_us",
			Help:    "Matching engine order execution latency in microseconds.",
			Buckets: []float64{0.25, 0.5, 1, 2, 5, 10, 25, 50, 100, 250, 500, 1000},
		}),
		IndexerBlockLag: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "indexer_block_lag",
			Help: "Block lag between chain head and canonical indexed block.",
		}),
		ReorgRollbacksTotal: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "reorg_rollbacks_total",
			Help: "Total number of EVM chain reorganization rollbacks executed.",
		}),
		StuckTxGasBumps: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "stuck_tx_gas_bumps",
			Help: "Total number of EIP-1559 stuck withdrawal transaction gas bumps.",
		}),
		ReservesInvariantOK: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "reserves_invariant_ok",
			Help: "1 if on-chain EscrowVault reserves >= total user liabilities + fees, else 0.",
		}),
		ActiveRPCEndpoint: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "active_rpc_endpoint",
			Help: "Indicator (1=active, 0=inactive) for configured EVM RPC endpoints.",
		}, []string{"endpoint"}),
		CircuitBreakerTripped: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "circuit_breaker_tripped",
			Help: "1 if the market circuit breaker is currently tripped, else 0.",
		}, []string{"market"}),
		latencyWindow: make([]int64, 0, 1024),
		activeRPCName: "rpc-proxy-1",
	}

	r.invariantOK.Store(true)
	r.ReservesInvariantOK.Set(1)
	r.ActiveRPCEndpoint.WithLabelValues("rpc-proxy-1").Set(1)
	r.ActiveRPCEndpoint.WithLabelValues("rpc-proxy-2").Set(0)
	r.CircuitBreakerTripped.WithLabelValues("ETH/USDC").Set(0)
	r.CircuitBreakerTripped.WithLabelValues("BTC/USDC").Set(0)

	reg.MustRegister(
		r.OrderMatchLatencyUs,
		r.IndexerBlockLag,
		r.ReorgRollbacksTotal,
		r.StuckTxGasBumps,
		r.ReservesInvariantOK,
		r.ActiveRPCEndpoint,
		r.CircuitBreakerTripped,
	)

	return r
}

// Default is the process-wide metrics registry.
var Default = NewRegistry()

// PromRegistry returns the underlying Prometheus registry.
func (r *Registry) PromRegistry() *prometheus.Registry {
	return r.promReg
}

// Handler returns an HTTP handler serving Prometheus metrics.
func (r *Registry) Handler() http.Handler {
	return promhttp.HandlerFor(r.promReg, promhttp.HandlerOpts{})
}

// RecordMatchLatencyNs records a match latency sample in nanoseconds.
func (r *Registry) RecordMatchLatencyNs(ns int64) {
	if ns < 0 {
		ns = 0
	}
	us := float64(ns) / 1000.0
	r.OrderMatchLatencyUs.Observe(us)

	r.mu.Lock()
	if len(r.latencyWindow) >= 1024 {
		copy(r.latencyWindow, r.latencyWindow[1:])
		r.latencyWindow[len(r.latencyWindow)-1] = ns
	} else {
		r.latencyWindow = append(r.latencyWindow, ns)
	}
	r.mu.Unlock()
}

// SetBlockLag updates indexer_block_lag.
func (r *Registry) SetBlockLag(lag int64) {
	if lag < 0 {
		lag = 0
	}
	r.blockLag.Store(lag)
	r.IndexerBlockLag.Set(float64(lag))
}

// IncReorgRollbacks increments reorg_rollbacks_total.
func (r *Registry) IncReorgRollbacks() {
	r.reorgCount.Add(1)
	r.ReorgRollbacksTotal.Inc()
}

// IncStuckTxGasBumps increments stuck_tx_gas_bumps.
func (r *Registry) IncStuckTxGasBumps() {
	r.gasBumpCount.Add(1)
	r.StuckTxGasBumps.Inc()
}

// SetReservesInvariant updates reserves_invariant_ok.
func (r *Registry) SetReservesInvariant(ok bool) {
	r.invariantOK.Store(ok)
	if ok {
		r.ReservesInvariantOK.Set(1)
	} else {
		r.ReservesInvariantOK.Set(0)
	}
}

// SetActiveRPC updates active_rpc_endpoint gauges.
func (r *Registry) SetActiveRPC(active string, allEndpoints ...string) {
	r.mu.Lock()
	r.activeRPCName = active
	r.mu.Unlock()
	for _, ep := range allEndpoints {
		if ep == active {
			r.ActiveRPCEndpoint.WithLabelValues(ep).Set(1)
		} else {
			r.ActiveRPCEndpoint.WithLabelValues(ep).Set(0)
		}
	}
	if len(allEndpoints) == 0 {
		r.ActiveRPCEndpoint.WithLabelValues(active).Set(1)
	}
}

// SetCircuitBreaker updates circuit_breaker_tripped for a market.
func (r *Registry) SetCircuitBreaker(market string, tripped bool) {
	r.cbTripped.Store(tripped)
	if tripped {
		r.CircuitBreakerTripped.WithLabelValues(market).Set(1)
	} else {
		r.CircuitBreakerTripped.WithLabelValues(market).Set(0)
	}
}

// TelemetrySnapshot captures non-financial operational telemetry for the WebSocket strip.
type TelemetrySnapshot struct {
	P50Us                string `json:"clob_p50_us"`
	P99Us                string `json:"clob_p99_us"`
	P999Us               string `json:"clob_p999_us"`
	ActiveRPC            string `json:"active_rpc"`
	IndexerBlockLag      int64  `json:"indexer_block_lag"`
	ReorgRollbacksTotal  int64  `json:"reorg_rollbacks_total"`
	StuckTxGasBumps      int64  `json:"stuck_tx_gas_bumps"`
	ReservesInvariantOK  bool   `json:"reserves_invariant_ok"`
	CircuitBreakerStatus string `json:"circuit_breaker_status"`
}

// Snapshot returns the current telemetry summary.
func (r *Registry) Snapshot() TelemetrySnapshot {
	r.mu.Lock()
	samples := make([]int64, len(r.latencyWindow))
	copy(samples, r.latencyWindow)
	activeRPC := r.activeRPCName
	r.mu.Unlock()

	p50, p99, p999 := "0.00", "0.00", "0.00"
	if len(samples) > 0 {
		slices.Sort(samples)
		p50 = formatNsAsUs(percentile(samples, 500))
		p99 = formatNsAsUs(percentile(samples, 990))
		p999 = formatNsAsUs(percentile(samples, 999))
	}

	cbStatus := "NORMAL"
	if r.cbTripped.Load() {
		cbStatus = "HALTED (>10% DEV)"
	}

	return TelemetrySnapshot{
		P50Us:                p50,
		P99Us:                p99,
		P999Us:               p999,
		ActiveRPC:            activeRPC,
		IndexerBlockLag:      r.blockLag.Load(),
		ReorgRollbacksTotal:  r.reorgCount.Load(),
		StuckTxGasBumps:      r.gasBumpCount.Load(),
		ReservesInvariantOK:  r.invariantOK.Load(),
		CircuitBreakerStatus: cbStatus,
	}
}

func percentile(sorted []int64, permille int) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := (len(sorted) - 1) * permille / 1000
	return sorted[idx]
}

func formatNsAsUs(ns int64) string {
	// Integer-only formatting to 2 decimal places of microseconds (zero float64).
	usHundredths := ns / 10
	whole := usHundredths / 100
	frac := usHundredths % 100
	if frac < 0 {
		frac = -frac
	}
	return itoa64(whole) + "." + twoDigits(frac)
}

func itoa64(v int64) string {
	if v == 0 {
		return "0"
	}
	var buf [24]byte
	i := len(buf)
	neg := v < 0
	if neg {
		v = -v
	}
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}

func twoDigits(v int64) string {
	return string([]byte{byte('0' + (v/10)%10), byte('0' + v%10)})
}
