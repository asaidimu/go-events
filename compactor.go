package events

import (
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cockroachdb/pebble"
	"github.com/gofrs/uuid/v5"
)

// CompactionRequest is passed by the bus to Compactor.Compact on every
// compaction tick. It carries everything the compactor needs to make a
// safe, informed decision about what to delete.
type CompactionRequest struct {
	// BusKey is the identifier of the bus requesting compaction.
	BusKey string

	// SafeFrontier is the minimum checkpoint UUID across all active
	// subscribers. The compactor MUST NOT delete any event at or after
	// this key — doing so would cause those subscribers to miss events.
	//
	// A nil/zero SafeFrontier means no subscribers have checkpointed yet;
	// the compactor should treat this as "compact nothing".
	SafeFrontier uuid.UUID

	// SafeFrontierIsZero is true when no subscriber has ever checkpointed,
	// i.e. SafeFrontier has not been set. Compactors should skip compaction
	// entirely in this case.
	SafeFrontierIsZero bool

	// EventDB is a direct handle to the event log Pebble instance.
	// The compactor uses it to scan, read, and delete event keys.
	EventDB *pebble.DB

	// ArchiveDir is the directory where the default compactor writes
	// compressed archive files. Custom compactors may ignore this.
	ArchiveDir string
}

// Compactor decides which events are eligible for removal from the live
// event log, optionally archives them, and performs the deletion.
//
// Compact is called on a regular schedule by the bus. It must respect
// req.SafeFrontier — events at or after that key must not be deleted.
//
// Compact must be safe to call concurrently (the bus calls it from a
// single goroutine, but custom implementations may spawn workers).
type Compactor interface {
	Compact(ctx context.Context, req CompactionRequest) error
}

// ---- Default compactor: gzip NDJSON archive ----

// ArchiveCompactorConfig configures the default compactor.
type ArchiveCompactorConfig struct {
	// RetentionPeriod is the minimum age an event must reach before it is
	// eligible for compaction. Events newer than this are always kept.
	// Default: 72h
	RetentionPeriod time.Duration

	// ArchiveDir is the directory where gzip-compressed NDJSON archive
	// files are written before deletion from Pebble.
	// Each compaction run writes one file named:
	//   {busKey}-{RFC3339-compaction-time}.ndjson.gz
	// Default: {baseDir}/{busKey}/archive/
	ArchiveDir string
}

// archiveRecord is one line in the NDJSON archive file.
type archiveRecord struct {
	SequenceID string `json:"seq"`
	Topic      string `json:"topic"`
	Payload    []byte `json:"payload"`
	EventTime  string `json:"event_time"`
	ArchivedAt string `json:"archived_at"`
}

// ArchiveCompactor is the default Compactor implementation. On each run it:
//  1. Computes the deletion ceiling: min(SafeFrontier, cutoffByAge)
//  2. Scans event keys in [evtPrefix, ceiling)
//  3. Writes matching events to a gzip NDJSON archive file
//  4. Deletes the scanned range from Pebble with DeleteRange
//  5. Hints Pebble to compact the freed LSM space
type ArchiveCompactor struct {
	cfg ArchiveCompactorConfig
}

// NewArchiveCompactor creates a default compactor with the given config.
// If cfg.RetentionPeriod is zero it defaults to 72 hours.
func NewArchiveCompactor(cfg ArchiveCompactorConfig) *ArchiveCompactor {
	if cfg.RetentionPeriod == 0 {
		cfg.RetentionPeriod = 72 * time.Hour
	}
	return &ArchiveCompactor{cfg: cfg}
}

// Compact archives events before the safe frontier to gzipped NDJSON and
// deletes them from the event log. Returns the number of events compacted.
func (c *ArchiveCompactor) Compact(ctx context.Context, req CompactionRequest) error {
	if req.SafeFrontierIsZero {
		// No subscriber has checkpointed yet — unsafe to compact anything.
		return nil
	}

	archiveDir := c.cfg.ArchiveDir
	if archiveDir == "" {
		archiveDir = req.ArchiveDir
	}
	if err := os.MkdirAll(archiveDir, 0o755); err != nil {
		return fmt.Errorf("compactor: mkdir %q: %w", archiveDir, err)
	}

	// Compute the age-based cutoff key: a UUIDv7 whose timestamp equals
	// now - RetentionPeriod. Events before this key are old enough to compact.
	cutoffTime := time.Now().Add(-c.cfg.RetentionPeriod)
	cutoffUUID := UUIDForTime(cutoffTime)
	cutoffKey := makeEvtKey(cutoffUUID)

	// The safe deletion ceiling is the lesser of the age cutoff and the
	// subscriber frontier. We use Pebble's byte ordering to pick the minimum.
	frontierKey := makeEvtKey(req.SafeFrontier)
	ceilingKey := cutoffKey
	if comparePebbleKeys(frontierKey, cutoffKey) < 0 {
		// SafeFrontier is older than the age cutoff — use it as the ceiling.
		ceilingKey = frontierKey
	}

	// Scan [evtPrefix, ceilingKey) and collect events to archive.
	evtPrefix := []byte("evt:")
	iter, err := req.EventDB.NewIter(&pebble.IterOptions{
		LowerBound: evtPrefix,
		UpperBound: ceilingKey,
	})
	if err != nil {
		return fmt.Errorf("compactor: scan iterator: %w", err)
	}

	now := time.Now().UTC()
	archiveName := filepath.Join(
		archiveDir,
		fmt.Sprintf("%s-%s.ndjson.gz", req.BusKey, now.Format("20060102T150405Z")),
	)

	f, err := os.Create(archiveName)
	if err != nil {
		iter.Close()
		return fmt.Errorf("compactor: create archive %q: %w", archiveName, err)
	}

	gz := gzip.NewWriter(f)
	enc := json.NewEncoder(gz)
	archivedAt := now.Format(time.RFC3339)
	var count int

	for iter.First(); iter.Valid(); iter.Next() {
		select {
		case <-ctx.Done():
			iter.Close()
			_ = gz.Close()
			_ = f.Close()
			return ctx.Err()
		default:
		}

		rawKey := make([]byte, len(iter.Key()))
		copy(rawKey, iter.Key())
		rawVal := make([]byte, len(iter.Value()))
		copy(rawVal, iter.Value())

		eventID, parseErr := parseEvtKey(rawKey)
		if parseErr != nil {
			// Skip corrupt keys — they will still be deleted below.
			continue
		}
		topic, payload, decodeErr := decodeValue(rawVal)
		if decodeErr != nil {
			continue
		}

		rec := archiveRecord{
			SequenceID: eventID.String(),
			Topic:      topic,
			Payload:    payload,
			EventTime:  uuidV7Time(eventID).Format(time.RFC3339Nano),
			ArchivedAt: archivedAt,
		}
		if encErr := enc.Encode(rec); encErr != nil {
			iter.Close()
			_ = gz.Close()
			_ = f.Close()
			return fmt.Errorf("compactor: encode record: %w", encErr)
		}
		count++
	}

	iter.Close()

	if err := gz.Close(); err != nil {
		_ = f.Close()
		return fmt.Errorf("compactor: close gzip writer: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("compactor: close archive file: %w", err)
	}

	// If nothing was archived, remove the empty file and return.
	if count == 0 {
		_ = os.Remove(archiveName)
		return nil
	}

	// Delete the archived range from Pebble.
	if err := req.EventDB.DeleteRange(evtPrefix, ceilingKey, pebble.Sync); err != nil {
		return fmt.Errorf("compactor: delete range: %w", err)
	}

	// Hint Pebble to reclaim LSM space freed by the deletion.
	if err := req.EventDB.Compact(evtPrefix, ceilingKey, true); err != nil {
		// Non-fatal — compaction will happen eventually on its own.
		_ = err
	}

	return nil
}

// NoopCompactor is a Compactor that does nothing. Useful for testing or when
// external retention is managed by a separate process.
type NoopCompactor struct{}

// Compact is a no-op. It always returns nil.
func (NoopCompactor) Compact(_ context.Context, _ CompactionRequest) error { return nil }

// ---- Key-space helpers used by the compactor ----

// UUIDForTime constructs a UUIDv7 whose 48-bit timestamp field encodes t.
// The random suffix is all zeros — this gives us the lexicographically smallest
// UUIDv7 for that millisecond, which is correct as an exclusive upper bound.
//
// Useful with SubscribeOptions.StartAt to subscribe from a wall-clock time:
//
//	opts.StartAt = events.UUIDForTime(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
func UUIDForTime(t time.Time) uuid.UUID {
	ms := uint64(t.UnixMilli())
	var id uuid.UUID
	// Encode 48-bit ms timestamp into bytes 0–5.
	id[0] = byte(ms >> 40)
	id[1] = byte(ms >> 32)
	id[2] = byte(ms >> 24)
	id[3] = byte(ms >> 16)
	id[4] = byte(ms >> 8)
	id[5] = byte(ms)
	// Set version (4 bits) = 7 in the high nibble of byte 6.
	id[6] = 0x70
	// Variant bits: 10xx xxxx in byte 8.
	id[8] = 0x80
	return id
}

// comparePebbleKeys returns negative, zero, or positive like bytes.Compare.
// Used by the compactor to pick the lesser of two ceiling keys.
func comparePebbleKeys(a, b []byte) int {
	minLen := len(a)
	if len(b) < minLen {
		minLen = len(b)
	}
	for i := 0; i < minLen; i++ {
		if a[i] < b[i] {
			return -1
		}
		if a[i] > b[i] {
			return 1
		}
	}
	return len(a) - len(b)
}

// Ensure NoopCompactor satisfies the interface at compile time.
var _ Compactor = NoopCompactor{}
var _ Compactor = (*ArchiveCompactor)(nil)

// Ensure pebbleStore satisfies Store at compile time.
var _ Store = (*pebbleStore)(nil)

// suppress unused import if errors is only used via ErrStoreKeyNotFound
var _ = errors.New
