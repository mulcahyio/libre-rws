package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"sync"
	"time"
)

// Sizes of the in-memory views of the log (DCM-SRS-084, -085, -096). They are
// fixed: a control request can never make the node hold more than this.
const (
	LogRingSize   = 2000 // most recent log lines that are not audit records
	AuditRingSize = 1000 // most recent audit records (audit=true)
	// maxRingLine caps one stored line. A longer line is still written to the
	// log; the buffer keeps a short marker instead.
	maxRingLine = 8 << 10
)

// LogEntry is one record held in a LogBuffer. Line is the record as a JSON
// object, exactly the fields the logger wrote; nothing is added.
type LogEntry struct {
	Time    time.Time
	Level   slog.Level
	Event   string // audit records: the "event" field
	Outcome string // audit records: the "outcome" field
	Line    json.RawMessage
}

// LogBuffer is a fixed-size ring of the most recent log records. It is a
// convenience view for the control API: the log stream stays the record of
// the site, and the buffer is lost at restart.
type LogBuffer struct {
	mu   sync.Mutex
	buf  []LogEntry
	next int // index of the slot the next record goes to
	n    int // records held (<= len(buf))
}

// NewLogBuffer makes a ring that holds at most size records.
func NewLogBuffer(size int) *LogBuffer {
	if size < 1 {
		size = 1
	}
	return &LogBuffer{buf: make([]LogEntry, size)}
}

// Size is the fixed capacity.
func (b *LogBuffer) Size() int { return len(b.buf) }

// Len is the number of records held now.
func (b *LogBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.n
}

func (b *LogBuffer) add(e LogEntry) {
	b.mu.Lock()
	b.buf[b.next] = e
	b.next = (b.next + 1) % len(b.buf)
	if b.n < len(b.buf) {
		b.n++
	}
	b.mu.Unlock()
}

// Select returns the newest records for which match is true, at most limit
// of them, oldest first. more reports that older matching records were left
// out because of limit. A nil match selects every record.
func (b *LogBuffer) Select(limit int, match func(*LogEntry) bool) (out []LogEntry, more bool) {
	if limit < 1 {
		return nil, false
	}
	b.mu.Lock()
	for i := 1; i <= b.n; i++ {
		e := &b.buf[(b.next-i+len(b.buf))%len(b.buf)]
		if match != nil && !match(e) {
			continue
		}
		if len(out) == limit {
			more = true
			break
		}
		out = append(out, *e)
	}
	b.mu.Unlock()
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}
	return out, more
}

// ringSink receives each record as one JSON line from the encoder and files
// it under the facts taken from the record itself.
type ringSink struct {
	mu           sync.Mutex
	cur          LogEntry
	audit        bool
	logs, audits *LogBuffer
}

func (s *ringSink) Write(p []byte) (int, error) {
	line := bytes.TrimSpace(p)
	e := s.cur
	if len(line) > maxRingLine {
		e.Line, _ = json.Marshal(map[string]any{"time": e.Time, "level": e.Level.String(),
			"msg": "line too long for the in-memory buffer; see the log", "bytes": len(line)})
	} else {
		e.Line = append(json.RawMessage(nil), line...)
	}
	if s.audit {
		s.audits.add(e)
	} else {
		s.logs.add(e)
	}
	return len(p), nil
}

// ringHandler passes every record to the real log handler first and then
// keeps a copy in the ring. The ring can never stop or change a log line.
type ringHandler struct {
	next slog.Handler // writes the log
	enc  slog.Handler // JSON encoder that writes into sink
	sink *ringSink
}

func newRingHandler(next slog.Handler, logs, audits *LogBuffer) slog.Handler {
	sink := &ringSink{logs: logs, audits: audits}
	// The level is decided by next.Enabled; the encoder itself drops nothing.
	enc := slog.NewJSONHandler(sink, &slog.HandlerOptions{Level: slog.Level(-1 << 20)})
	return &ringHandler{next: next, enc: enc, sink: sink}
}

func (h *ringHandler) Enabled(ctx context.Context, l slog.Level) bool { return h.next.Enabled(ctx, l) }

func (h *ringHandler) Handle(ctx context.Context, rec slog.Record) error {
	err := h.next.Handle(ctx, rec)
	e := LogEntry{Time: rec.Time, Level: rec.Level}
	audit := false
	rec.Attrs(func(a slog.Attr) bool {
		switch a.Key {
		case "audit":
			audit = a.Value.Kind() == slog.KindBool && a.Value.Bool()
		case "event":
			e.Event = a.Value.String()
		case "outcome":
			e.Outcome = a.Value.String()
		}
		return true
	})
	h.sink.mu.Lock()
	h.sink.cur, h.sink.audit = e, audit
	_ = h.enc.Handle(ctx, rec)
	h.sink.mu.Unlock()
	return err
}

func (h *ringHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &ringHandler{next: h.next.WithAttrs(attrs), enc: h.enc.WithAttrs(attrs), sink: h.sink}
}

func (h *ringHandler) WithGroup(name string) slog.Handler {
	return &ringHandler{next: h.next.WithGroup(name), enc: h.enc.WithGroup(name), sink: h.sink}
}
