package events

import (
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"rws/modules/dicom/internal/index"
)

// Outbox is the part of the index the relay needs.
type Outbox interface {
	ClaimEvents(ctx context.Context, limit int, lease time.Duration) ([]index.OutboxEvent, error)
	AckEvent(ctx context.Context, msgID string) error
	NackEvent(ctx context.Context, msgID string, retryAt time.Time, errText string) error
	PendingEvents(ctx context.Context) (int, error)
}

// Relay publishes outbox events to the bus. Events are written to the outbox
// in the same transaction as the instance, so an event bus outage delays
// events instead of losing them. Each event keeps its message id across
// retries, and JetStream de-duplicates on it.
type Relay struct {
	ob      Outbox
	pub     Publisher
	log     *slog.Logger
	onFail  func()
	nudge   chan struct{}
	pending prometheus.Gauge

	// Tunables (tests shorten them).
	Interval time.Duration // poll interval when idle
	Batch    int
	Lease    time.Duration // how long a claimed event is reserved
	MaxDelay time.Duration // retry back-off ceiling
}

// NewRelay builds a relay. onFail (optional) is called for every failed
// publish attempt, for metrics.
func NewRelay(ob Outbox, pub Publisher, log *slog.Logger, reg prometheus.Registerer, onFail func()) *Relay {
	r := &Relay{
		ob: ob, pub: pub, log: log.With("component", "event-outbox"), onFail: onFail,
		nudge: make(chan struct{}, 1),
		pending: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "rws_dicom_outbox_pending", Help: "Events stored but not yet published to the bus.",
		}),
		Interval: 2 * time.Second, Batch: 100, Lease: 30 * time.Second, MaxDelay: 5 * time.Minute,
	}
	if reg != nil {
		reg.MustRegister(r.pending)
	}
	return r
}

func (r *Relay) Name() string { return "event-outbox" }

// Nudge asks the relay to drain now instead of at the next poll.
func (r *Relay) Nudge() {
	select {
	case r.nudge <- struct{}{}:
	default:
	}
}

// Run drains the outbox until ctx is cancelled.
func (r *Relay) Run(ctx context.Context) error {
	t := time.NewTicker(r.Interval)
	defer t.Stop()
	for {
		r.drain(ctx)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
		case <-r.nudge:
		}
	}
}

// drain publishes due events in batches until none are due or one fails.
func (r *Relay) drain(ctx context.Context) {
	defer r.updatePending(ctx)
	for ctx.Err() == nil {
		evs, err := r.ob.ClaimEvents(ctx, r.Batch, r.Lease)
		if err != nil {
			r.log.Warn("claim outbox events", "err", err)
			return
		}
		if len(evs) == 0 {
			return
		}
		for _, e := range evs {
			pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			err := r.pub.Publish(pctx, e.Subject, e.MsgID, json.RawMessage(e.Payload))
			cancel()
			if err != nil {
				if r.onFail != nil {
					r.onFail()
				}
				retry := time.Now().Add(r.backoff(e.Attempts))
				if nerr := r.ob.NackEvent(ctx, e.MsgID, retry, err.Error()); nerr != nil {
					r.log.Warn("reschedule outbox event", "msg_id", e.MsgID, "err", nerr)
				}
				r.log.Warn("publish failed; will retry", "msg_id", e.MsgID, "attempt", e.Attempts, "retry_at", retry, "err", err)
				return // the bus is probably down; leave the rest leased until it recovers
			}
			if err := r.ob.AckEvent(ctx, e.MsgID); err != nil {
				r.log.Warn("ack outbox event", "msg_id", e.MsgID, "err", err)
			}
		}
	}
}

// backoff doubles from 1s per attempt, capped at MaxDelay.
func (r *Relay) backoff(attempts int) time.Duration {
	d := time.Second
	for i := 1; i < attempts && d < r.MaxDelay; i++ {
		d *= 2
	}
	return min(d, r.MaxDelay)
}

func (r *Relay) updatePending(ctx context.Context) {
	if n, err := r.ob.PendingEvents(ctx); err == nil {
		r.pending.Set(float64(n))
	}
}
