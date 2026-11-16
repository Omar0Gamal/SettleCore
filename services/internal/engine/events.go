package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
	"path/filepath"
	"sync"

	"github.com/google/uuid"
)

const (
	// WALMagic is "SCWL" in big-endian (0x5343574C) stored at bytes [0..3].
	WALMagic uint32 = 0x5343574C
	// WALVersion is the binary WAL format version.
	WALVersion uint8 = 1
	// WALRecordSize is the fixed 64-byte WAL record size per ADR-005.
	WALRecordSize = 64

	// EventLogMagic is "SCEV" in big-endian (0x53434556) stored at bytes [0..3].
	EventLogMagic uint32 = 0x53434556
	// EventLogVersion is the binary execution event log version per ADR-006.
	EventLogVersion uint8 = 1
	// EventRecordSize is the fixed 64-byte execution event record size.
	EventRecordSize = 64
)

// Execution event types matching C++ WalEventRecord (ADR-006).
const (
	EventTypeOrderAccepted uint8 = 1
	EventTypeTradeFill     uint8 = 2
	EventTypeOrderCanceled uint8 = 3
	EventTypeOrderRejected uint8 = 4
)

// Execution event flags matching C++ WalEventRecord.
const (
	EventFlagSideSell      uint8 = 1 << 0
	EventFlagMakerTerminal uint8 = 1 << 1
	EventFlagTakerTerminal uint8 = 1 << 2
)

// WAL opcodes matching ADR-005.
const (
	OpSubmitOrder      uint8 = 0x01
	OpCancelOrder      uint8 = 0x02
	OpCancelAllAccount uint8 = 0x03
	OpUpdateMarkPrice  uint8 = 0x04
	OpSnapshotBarrier  uint8 = 0x05
)

// Market numeric IDs matching ADR-002.
const (
	MarketCodeETHUSDC uint16 = 1
	MarketCodeBTCUSDC uint16 = 2
)

// Side, order type, time-in-force, and STP numeric codes matching ADR-002.
const (
	SideCodeBuy  uint8 = 0
	SideCodeSell uint8 = 1

	OrderTypeCodeLimit  uint8 = 0
	OrderTypeCodeMarket uint8 = 1

	TIFCodeGTC uint8 = 0
	TIFCodeIOC uint8 = 1
	TIFCodeFOK uint8 = 2

	STPCodeCancelNewest uint8 = 0
	STPCodeCancelOldest uint8 = 1
	STPCodeCancelBoth   uint8 = 2
)

// ExecReport status codes matching ADR-002.
const (
	ExecStatusAccepted        uint8 = 0
	ExecStatusPartiallyFilled uint8 = 1
	ExecStatusFilled          uint8 = 2
	ExecStatusCanceled        uint8 = 3
	ExecStatusSTPCanceled     uint8 = 4
	ExecStatusCBRejected      uint8 = 5
)

// Engine error codes.
const (
	ErrCodeOK                    int32 = 0
	ErrCodeInvalidNumericBound   int32 = -1
	ErrCodeCircuitBreakerTripped int32 = -2
	ErrCodeOrderPoolExhausted    int32 = -3
	ErrCodeOrderNotFound         int32 = -4
	ErrCodeFOKUnfilled           int32 = -5
	ErrCodeBackpressure          int32 = -6
	ErrCodeSTPRejected           int32 = -7
)

	var (
	// ErrInvalidWALMagic indicates corrupt or non-SCWL WAL bytes.
	ErrInvalidWALMagic = errors.New("invalid WAL record magic")
	// ErrInvalidWALCRC indicates a CRC32C checksum mismatch in a WAL frame.
	ErrInvalidWALCRC = errors.New("WAL CRC32C mismatch")
	// ErrInvalidWALSize indicates buffer is not 64 bytes.
	ErrInvalidWALSize = errors.New("WAL record must be 64 bytes")
	// ErrInvalidEventMagic indicates corrupt or non-SCEV event log bytes.
	ErrInvalidEventMagic = errors.New("invalid event log record magic")
	// ErrInvalidEventCRC indicates a CRC32C checksum mismatch in an event log frame.
	ErrInvalidEventCRC = errors.New("event log CRC32C mismatch")
	// ErrInvalidEventSize indicates buffer is not 64 bytes.
	ErrInvalidEventSize = errors.New("event log record must be 64 bytes")
)

var crc32cTable = crc32.MakeTable(crc32.Castagnoli)

// WALRecord represents the fixed 64-byte binary WAL frame specified in ADR-005.
type WALRecord struct {
	Magic         uint32
	Version       uint8
	Opcode        uint8
	MarketID      uint16
	Flags         uint16
	Reserved      uint16
	CRC32C        uint32
	WalSeq        uint64
	OrderID       uint64
	AccountIDLow  uint64
	AccountIDHigh uint64
	Price1e8      int64
	Qty1e8        int64
}

// PackFlags packs side, orderType, timeInForce, and stpMode into a uint16 bitfield.
func PackFlags(side, orderType, tif, stp uint8) uint16 {
	return uint16(side&0x03) |
		(uint16(orderType&0x03) << 2) |
		(uint16(tif&0x03) << 4) |
		(uint16(stp&0x03) << 6)
}

// UnpackFlags extracts side, orderType, timeInForce, and stpMode from a uint16 bitfield.
func UnpackFlags(flags uint16) (side, orderType, tif, stp uint8) {
	side = uint8(flags & 0x03)
	orderType = uint8((flags >> 2) & 0x03)
	tif = uint8((flags >> 4) & 0x03)
	stp = uint8((flags >> 6) & 0x03)
	return side, orderType, tif, stp
}

// MarshalBinary encodes the WALRecord into an exact 64-byte little-endian frame with CRC32C.
func (r WALRecord) MarshalBinary() [WALRecordSize]byte {
	var buf [WALRecordSize]byte
	binary.LittleEndian.PutUint32(buf[0:4], WALMagic)
	buf[4] = WALVersion
	buf[5] = r.Opcode
	binary.LittleEndian.PutUint16(buf[6:8], r.MarketID)
	binary.LittleEndian.PutUint16(buf[8:10], r.Flags)
	binary.LittleEndian.PutUint16(buf[10:12], 0)
	binary.LittleEndian.PutUint64(buf[16:24], r.WalSeq)
	binary.LittleEndian.PutUint64(buf[24:32], r.OrderID)
	binary.LittleEndian.PutUint64(buf[32:40], r.AccountIDLow)
	binary.LittleEndian.PutUint64(buf[40:48], r.AccountIDHigh)
	binary.LittleEndian.PutUint64(buf[48:56], uint64(r.Price1e8))
	binary.LittleEndian.PutUint64(buf[56:64], uint64(r.Qty1e8))
	sum := crc32.Checksum(buf[16:64], crc32cTable)
	binary.LittleEndian.PutUint32(buf[12:16], sum)
	return buf
}

// UnmarshalWALRecord decodes and validates a 64-byte WAL record.
func UnmarshalWALRecord(buf []byte) (WALRecord, error) {
	if len(buf) != WALRecordSize {
		return WALRecord{}, ErrInvalidWALSize
	}
	magic := binary.LittleEndian.Uint32(buf[0:4])
	if magic != WALMagic {
		return WALRecord{}, fmt.Errorf("%w: 0x%x", ErrInvalidWALMagic, magic)
	}
	expectedCRC := binary.LittleEndian.Uint32(buf[12:16])
	actualCRC := crc32.Checksum(buf[16:64], crc32cTable)
	if expectedCRC != actualCRC {
		return WALRecord{}, fmt.Errorf("%w: got 0x%x want 0x%x", ErrInvalidWALCRC, actualCRC, expectedCRC)
	}
	return WALRecord{
		Magic:         magic,
		Version:       buf[4],
		Opcode:        buf[5],
		MarketID:      binary.LittleEndian.Uint16(buf[6:8]),
		Flags:         binary.LittleEndian.Uint16(buf[8:10]),
		Reserved:      binary.LittleEndian.Uint16(buf[10:12]),
		CRC32C:        expectedCRC,
		WalSeq:        binary.LittleEndian.Uint64(buf[16:24]),
		OrderID:       binary.LittleEndian.Uint64(buf[24:32]),
		AccountIDLow:  binary.LittleEndian.Uint64(buf[32:40]),
		AccountIDHigh: binary.LittleEndian.Uint64(buf[40:48]),
		Price1e8:      int64(binary.LittleEndian.Uint64(buf[48:56])),
		Qty1e8:        int64(binary.LittleEndian.Uint64(buf[56:64])),
	}, nil
}

// WALWriter appends 64-byte WAL records with configurable durability mode ("memory", "buffered", "fsync").
type WALWriter struct {
	mu       sync.Mutex
	mode     string
	filePath string
	file     *os.File
	records  []WALRecord
}

// NewWALWriter opens or initializes a WALWriter in the given directory and mode.
func NewWALWriter(walDir, mode string) (*WALWriter, error) {
	if mode == "" {
		mode = "buffered"
	}
	w := &WALWriter{
		mode:    mode,
		records: make([]WALRecord, 0, 1024),
	}
	if mode == "memory" || walDir == "" {
		return w, nil
	}
	if err := os.MkdirAll(walDir, 0o750); err != nil {
		return nil, err
	}
	path := filepath.Join(walDir, "engine.wal")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o640) // #nosec G304
	if err != nil {
		return nil, err
	}
	w.filePath = path
	w.file = f
	return w, nil
}

// Append writes a WAL record atomically.
func (w *WALWriter) Append(rec WALRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	frame := rec.MarshalBinary()
	if w.file != nil {
		if _, err := w.file.Write(frame[:]); err != nil {
			return err
		}
		if w.mode == "fsync" {
			if err := w.file.Sync(); err != nil {
				return err
			}
		}
	}
	w.records = append(w.records, rec)
	return nil
}

// Sync flushes buffered writes to disk.
func (w *WALWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Sync()
	}
	return nil
}

// Close closes the underlying WAL file if open.
func (w *WALWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

// Path returns the WAL file path (empty in memory mode).
func (w *WALWriter) Path() string {
	return w.filePath
}

// ReadWAL reads all valid 64-byte WAL records from path. If a torn tail (<64 bytes
// or bad CRC at the very end) is detected, it truncates the torn tail per ADR-005.
func ReadWAL(path string) ([]WALRecord, bool, error) {
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	fullFrames := len(data) / WALRecordSize
	tornTail := len(data)%WALRecordSize != 0
	records := make([]WALRecord, 0, fullFrames)
	validBytes := 0

	for i := 0; i < fullFrames; i++ {
		off := i * WALRecordSize
		rec, err := UnmarshalWALRecord(data[off : off+WALRecordSize])
		if err != nil {
			// If this is the final trailing frame, treat as torn write and truncate.
			if i == fullFrames-1 {
				tornTail = true
				break
			}
			return nil, false, fmt.Errorf("corrupt WAL frame at index %d: %w", i, err)
		}
		records = append(records, rec)
		validBytes = off + WALRecordSize
	}

	if tornTail && len(data) > validBytes {
		if err := os.Truncate(path, int64(validBytes)); err != nil {
			return records, true, err
		}
	}
	return records, tornTail, nil
}

// OrderCmd mirrors sc_order_cmd_t from ADR-002.
type OrderCmd struct {
	WalSeq        uint64
	OrderID       uint64
	AccountIDLow  uint64
	AccountIDHigh uint64
	Price1e8      int64
	Qty1e8        int64
	TimestampNs   uint64
	MarketID      uint16
	Opcode        uint8
	Side          uint8
	OrderType     uint8
	TimeInForce   uint8
	STPMode       uint8
}

// FillEvent mirrors sc_fill_t from ADR-002.
type FillEvent struct {
	TradeID          uint64
	MakerOrderID     uint64
	TakerOrderID     uint64
	MakerAccountLow  uint64
	MakerAccountHigh uint64
	ExecPrice1e8     int64
	FillQty1e8       int64
	QuoteNotional1e8 int64
	MakerRemaining   int64
}

// ExecReport mirrors sc_exec_report_t from ADR-002.
type ExecReport struct {
	WalSeq          uint64
	OrderID         uint64
	FilledQty1e8    int64
	RemainingQty1e8 int64
	Fills           []FillEvent
	CanceledIDs     []uint64
	Status          uint8
	ErrorCode       int32
	LatencyNs       int64
}

// L2Level represents a price level on the order book.
type L2Level struct {
	Price1e8 int64
	Qty1e8   int64
}

// L2DepthSnapshot holds aggregated bid/ask levels for a market.
type L2DepthSnapshot struct {
	MarketID              string
	WalSeq                uint64
	MarkPrice1e8          int64
	CircuitBreakerTripped bool
	Bids                  []L2Level
	Asks                  []L2Level
}

// MarketIDToCode maps market ID strings to numeric C ABI codes.
func MarketIDToCode(marketID string) (uint16, error) {
	switch marketID {
	case "ETH/USDC":
		return MarketCodeETHUSDC, nil
	case "BTC/USDC":
		return MarketCodeBTCUSDC, nil
	default:
		return 0, fmt.Errorf("unsupported market: %s", marketID)
	}
}

// CodeToMarketID maps numeric C ABI codes back to market ID strings.
func CodeToMarketID(code uint16) (string, error) {
	switch code {
	case MarketCodeETHUSDC:
		return "ETH/USDC", nil
	case MarketCodeBTCUSDC:
		return "BTC/USDC", nil
	default:
		return "", fmt.Errorf("unsupported market code: %d", code)
	}
}

// UUIDToUint64Pair splits a 16-byte UUID into (low, high) uint64 pairs matching ADR-002.
func UUIDToUint64Pair(id uuid.UUID) (low uint64, high uint64) {
	high = binary.BigEndian.Uint64(id[0:8])
	low = binary.BigEndian.Uint64(id[8:16])
	return low, high
}

// Uint64PairToUUID reconstructs a UUID from (low, high) uint64 pairs.
func Uint64PairToUUID(low, high uint64) uuid.UUID {
	var id uuid.UUID
	binary.BigEndian.PutUint64(id[0:8], high)
	binary.BigEndian.PutUint64(id[8:16], low)
	return id
}

// WalEventRecord represents the fixed 64-byte binary execution event frame ("SCEV") matching C++ WalEventRecord.
type WalEventRecord struct {
	Magic        uint32
	Version      uint8
	EventType    uint8
	MarketID     uint16
	Status       uint8
	Flags        uint8
	Reserved     uint16
	CRC32C       uint32
	WalSeq       uint64
	OrderID      uint64
	MakerOrderID uint64
	TradeID      uint64
	ExecPrice1e8 int64
	FillQty1e8   int64
}

// MarshalBinary encodes the WalEventRecord into an exact 64-byte little-endian frame with CRC32C.
func (r WalEventRecord) MarshalBinary() [EventRecordSize]byte {
	var buf [EventRecordSize]byte
	binary.LittleEndian.PutUint32(buf[0:4], EventLogMagic)
	buf[4] = EventLogVersion
	buf[5] = r.EventType
	binary.LittleEndian.PutUint16(buf[6:8], r.MarketID)
	buf[8] = r.Status
	buf[9] = r.Flags
	binary.LittleEndian.PutUint16(buf[10:12], 0)
	binary.LittleEndian.PutUint64(buf[16:24], r.WalSeq)
	binary.LittleEndian.PutUint64(buf[24:32], r.OrderID)
	binary.LittleEndian.PutUint64(buf[32:40], r.MakerOrderID)
	binary.LittleEndian.PutUint64(buf[40:48], r.TradeID)
	binary.LittleEndian.PutUint64(buf[48:56], uint64(r.ExecPrice1e8))
	binary.LittleEndian.PutUint64(buf[56:64], uint64(r.FillQty1e8))
	sum := crc32.Checksum(buf[16:64], crc32cTable)
	binary.LittleEndian.PutUint32(buf[12:16], sum)
	return buf
}

// UnmarshalWalEventRecord decodes and validates a 64-byte "SCEV" execution event record.
func UnmarshalWalEventRecord(buf []byte) (WalEventRecord, error) {
	if len(buf) != EventRecordSize {
		return WalEventRecord{}, ErrInvalidEventSize
	}
	magic := binary.LittleEndian.Uint32(buf[0:4])
	if magic != EventLogMagic {
		return WalEventRecord{}, fmt.Errorf("%w: 0x%x", ErrInvalidEventMagic, magic)
	}
	expectedCRC := binary.LittleEndian.Uint32(buf[12:16])
	actualCRC := crc32.Checksum(buf[16:64], crc32cTable)
	if expectedCRC != actualCRC {
		return WalEventRecord{}, fmt.Errorf("%w: got 0x%x want 0x%x", ErrInvalidEventCRC, actualCRC, expectedCRC)
	}
	return WalEventRecord{
		Magic:        magic,
		Version:      buf[4],
		EventType:    buf[5],
		MarketID:     binary.LittleEndian.Uint16(buf[6:8]),
		Status:       buf[8],
		Flags:        buf[9],
		Reserved:     binary.LittleEndian.Uint16(buf[10:12]),
		CRC32C:       expectedCRC,
		WalSeq:       binary.LittleEndian.Uint64(buf[16:24]),
		OrderID:      binary.LittleEndian.Uint64(buf[24:32]),
		MakerOrderID: binary.LittleEndian.Uint64(buf[32:40]),
		TradeID:      binary.LittleEndian.Uint64(buf[40:48]),
		ExecPrice1e8: int64(binary.LittleEndian.Uint64(buf[48:56])),
		FillQty1e8:   int64(binary.LittleEndian.Uint64(buf[56:64])),
	}, nil
}

// EventLogWriter appends 64-byte "SCEV" execution event frames to <wal_path>.evlog.
type EventLogWriter struct {
	mu       sync.Mutex
	mode     string
	filePath string
	file     *os.File
	events   []WalEventRecord
}

// NewEventLogWriter opens or initializes an EventLogWriter at path (e.g. engine.wal.evlog).
func NewEventLogWriter(path, mode string) (*EventLogWriter, error) {
	if mode == "" {
		mode = "buffered"
	}
	w := &EventLogWriter{
		mode:   mode,
		events: make([]WalEventRecord, 0, 1024),
	}
	if mode == "memory" || path == "" {
		return w, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR|os.O_APPEND, 0o640) // #nosec G304
	if err != nil {
		return nil, err
	}
	w.filePath = path
	w.file = f
	return w, nil
}

// Append writes a 64-byte WalEventRecord atomically.
func (w *EventLogWriter) Append(rec WalEventRecord) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	frame := rec.MarshalBinary()
	if w.file != nil {
		if _, err := w.file.Write(frame[:]); err != nil {
			return err
		}
		if w.mode == "fsync" {
			if err := w.file.Sync(); err != nil {
				return err
			}
		}
	}
	w.events = append(w.events, rec)
	return nil
}

// Sync flushes buffered event log writes to disk.
func (w *EventLogWriter) Sync() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		return w.file.Sync()
	}
	return nil
}

// Close closes the underlying event log file if open.
func (w *EventLogWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file != nil {
		err := w.file.Close()
		w.file = nil
		return err
	}
	return nil
}

// Path returns the event log file path.
func (w *EventLogWriter) Path() string {
	return w.filePath
}

// WriteEventLog writes a slice of WalEventRecord frames to path atomically via append/create.
func WriteEventLog(path string, events []WalEventRecord) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	buf := make([]byte, 0, len(events)*EventRecordSize)
	for _, ev := range events {
		frame := ev.MarshalBinary()
		buf = append(buf, frame[:]...)
	}
	return os.WriteFile(path, buf, 0o640) // #nosec G306
}

// ReadEventLog reads all valid 64-byte "SCEV" execution event records from path.
// If a torn tail (<64 bytes or bad CRC on the trailing frame) is detected, it truncates the torn tail safely.
func ReadEventLog(path string) ([]WalEventRecord, bool, error) {
	data, err := os.ReadFile(path) // #nosec G304
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}

	fullFrames := len(data) / EventRecordSize
	tornTail := len(data)%EventRecordSize != 0
	events := make([]WalEventRecord, 0, fullFrames)
	validBytes := 0

	for i := 0; i < fullFrames; i++ {
		off := i * EventRecordSize
		rec, err := UnmarshalWalEventRecord(data[off : off+EventRecordSize])
		if err != nil {
			if i == fullFrames-1 {
				tornTail = true
				break
			}
			return nil, false, fmt.Errorf("corrupt event log frame at index %d: %w", i, err)
		}
		events = append(events, rec)
		validBytes = off + EventRecordSize
	}

	if tornTail && len(data) > validBytes {
		if err := os.Truncate(path, int64(validBytes)); err != nil {
			return events, true, err
		}
	}
	return events, tornTail, nil
}
