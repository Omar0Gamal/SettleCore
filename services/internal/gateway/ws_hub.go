package gateway

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/google/uuid"
)

const (
	wsOutboundBufferSize = 512
	wsHeartbeatInterval  = 3 * time.Second
)

// CancelOnDisconnectFunc is invoked when a WebSocket client with cancel_on_disconnect
// armed disconnects cleanly, drops TCP, times out heartbeat, or hits slow-consumer eviction.
type CancelOnDisconnectFunc func(ctx context.Context, userID uuid.UUID)

type wsClient struct {
	id                 uint64
	conn               *websocket.Conn
	userID             uuid.UUID
	cancelOnDisconnect atomic.Bool
	channels           map[string]struct{}
	chMu               sync.RWMutex
	outbound           chan []byte
	closeOnce          sync.Once
	done               chan struct{}
}

// WSHub manages multiplexed WebSocket subscriptions using github.com/coder/websocket
// with bounded 512-frame outbound queues and automatic Cancel-on-Disconnect.
type WSHub struct {
	mu           sync.RWMutex
	clients      map[uint64]*wsClient
	nextID       atomic.Uint64
	onDisconnect CancelOnDisconnectFunc
	codTriggered atomic.Int64
}

// NewWSHub constructs a WSHub.
func NewWSHub(onDisconnect CancelOnDisconnectFunc) *WSHub {
	return &WSHub{
		clients:      make(map[uint64]*wsClient),
		onDisconnect: onDisconnect,
	}
}

// CancelOnDisconnectCount returns how many automatic COD cancellations have fired (useful for tests/telemetry).
func (h *WSHub) CancelOnDisconnectCount() int64 {
	return h.codTriggered.Load()
}

// HasCancelOnDisconnectArmed reports whether an active WebSocket client for userID has COD armed.
func (h *WSHub) HasCancelOnDisconnectArmed(userID uuid.UUID) bool {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, c := range h.clients {
		c.chMu.RLock()
		uid := c.userID
		c.chMu.RUnlock()
		if uid == userID && c.cancelOnDisconnect.Load() {
			return true
		}
	}
	return false
}

type wsSubscribeMsg struct {
	Op                 string   `json:"op"`
	CancelOnDisconnect bool     `json:"cancel_on_disconnect"`
	UserID             string   `json:"user_id,omitempty"`
	Channels           []string `json:"channels"`
}

// ServeWS upgrades an HTTP connection to WebSocket and runs read/write/heartbeat loops.
func (h *WSHub) ServeWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true, // Allow localhost:3000 & local clients
	})
	if err != nil {
		return
	}

	userID := uuid.Nil
	if rawUID := r.Header.Get("X-SettleCore-User-Id"); rawUID != "" {
		if parsed, err := uuid.Parse(rawUID); err == nil {
			userID = parsed
		}
	}
	if qUID := r.URL.Query().Get("user_id"); qUID != "" && userID == uuid.Nil {
		if parsed, err := uuid.Parse(qUID); err == nil {
			userID = parsed
		}
	}

	client := &wsClient{
		id:       h.nextID.Add(1),
		conn:     conn,
		userID:   userID,
		channels: make(map[string]struct{}),
		outbound: make(chan []byte, wsOutboundBufferSize),
		done:     make(chan struct{}),
	}

	h.mu.Lock()
	h.clients[client.id] = client
	h.mu.Unlock()

	defer h.cleanupClient(client)

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	// Writer + heartbeat pump.
	go h.writePump(ctx, cancel, client)

	// Reader loop for subscribe/unsubscribe/ping commands.
	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		var msg wsSubscribeMsg
		if err := json.Unmarshal(data, &msg); err != nil {
			continue
		}
		switch msg.Op {
		case "subscribe":
			client.chMu.Lock()
			if msg.UserID != "" {
				if parsed, err := uuid.Parse(msg.UserID); err == nil {
					client.userID = parsed
				}
			}
			for _, ch := range msg.Channels {
				client.channels[ch] = struct{}{}
				// Also infer userID from "user:<uuid>" channel if not set.
				if client.userID == uuid.Nil && len(ch) > 5 && ch[:5] == "user:" {
					if parsed, err := uuid.Parse(ch[5:]); err == nil {
						client.userID = parsed
					}
				}
			}
			client.chMu.Unlock()
			if msg.CancelOnDisconnect {
				client.cancelOnDisconnect.Store(true)
			}
		case "unsubscribe":
			client.chMu.Lock()
			for _, ch := range msg.Channels {
				delete(client.channels, ch)
			}
			client.chMu.Unlock()
		}
	}
}

func (h *WSHub) writePump(ctx context.Context, cancel context.CancelFunc, client *wsClient) {
	ticker := time.NewTicker(wsHeartbeatInterval)
	defer ticker.Stop()
	defer cancel()

	for {
		select {
		case <-ctx.Done():
			return
		case <-client.done:
			return
		case payload := <-client.outbound:
			wctx, wcancel := context.WithTimeout(ctx, 2*time.Second)
			err := client.conn.Write(wctx, websocket.MessageText, payload)
			wcancel()
			if err != nil {
				return
			}
		case <-ticker.C:
			pctx, pcancel := context.WithTimeout(ctx, wsHeartbeatInterval)
			err := client.conn.Ping(pctx)
			pcancel()
			if err != nil {
				return
			}
		}
	}
}

func (h *WSHub) cleanupClient(client *wsClient) {
	client.closeOnce.Do(func() {
		close(client.done)
		_ = client.conn.Close(websocket.StatusNormalClosure, "closed")
		h.mu.Lock()
		delete(h.clients, client.id)
		h.mu.Unlock()

		client.chMu.RLock()
		uid := client.userID
		client.chMu.RUnlock()
		if client.cancelOnDisconnect.Load() && uid != uuid.Nil && h.onDisconnect != nil {
			codCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			h.onDisconnect(codCtx, uid)
			h.codTriggered.Add(1)
		}
	})
}

// Broadcast publishes a JSON-serializable payload to all clients subscribed to channel.
// Slow consumers whose 512-frame outbound ring fills are evicted with 1008 Policy Violation.
func (h *WSHub) Broadcast(channel string, payload any) {
	raw, err := json.Marshal(payload)
	if err != nil {
		return
	}

	h.mu.RLock()
	targets := make([]*wsClient, 0, len(h.clients))
	for _, c := range h.clients {
		c.chMu.RLock()
		_, sub := c.channels[channel]
		c.chMu.RUnlock()
		if sub {
			targets = append(targets, c)
		}
	}
	h.mu.RUnlock()

	for _, c := range targets {
		select {
		case c.outbound <- raw:
		default:
			// Slow consumer eviction with close code 1008 (architecture.md §3 #2).
			_ = c.conn.Close(websocket.StatusPolicyViolation, "Slow Consumer")
			h.cleanupClient(c)
		}
	}
}

// ConnectedCount returns the active WebSocket client count.
func (h *WSHub) ConnectedCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.clients)
}
