// Package events publishes what the DICOM module did, so the rest of RWS
// (routing, worklist reconciliation, AI, reporting, viewers) can react without
// being wired into the DICOM code. Subjects:
//
//	rws.dicom.instance.stored   one per received instance
//
// With NATS JetStream the stream is durable: a consumer that is down when an
// event is published receives it when it comes back.
package events

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
)

const SubjectInstanceStored = "rws.dicom.instance.stored"

// InstanceStored is the payload for SubjectInstanceStored.
type InstanceStored struct {
	EventID        string    `json:"event_id"` // SOP Instance UID + receive time; stable for de-duplication
	OccurredAt     time.Time `json:"occurred_at"`
	Node           string    `json:"node"`
	SourceAE       string    `json:"source_ae"`
	PatientID      string    `json:"patient_id"`
	StudyUID       string    `json:"study_instance_uid"`
	SeriesUID      string    `json:"series_instance_uid"`
	SOPInstanceUID string    `json:"sop_instance_uid"`
	SOPClassUID    string    `json:"sop_class_uid"`
	Modality       string    `json:"modality"`
	AccessionNum   string    `json:"accession_number"`
	TransferSyntax string    `json:"transfer_syntax_uid"`
	BlobKey        string    `json:"blob_key"`
	SizeBytes      int64     `json:"size_bytes"`
}

// Publisher sends events to the bus.
type Publisher interface {
	Publish(ctx context.Context, subject, msgID string, payload any) error
	Check(ctx context.Context) error
	Close() error
}

// Config selects and configures the publisher.
type Config struct {
	Driver     string `yaml:"driver" env:"RWS_EVENTS_DRIVER"` // none | log | nats
	URL        string `yaml:"url" env:"RWS_EVENTS_URL"`
	Stream     string `yaml:"stream" env:"RWS_EVENTS_STREAM"`
	Replicas   int    `yaml:"replicas" env:"RWS_EVENTS_REPLICAS"` // JetStream replicas (3 in a NATS cluster)
	MaxAgeDays int    `yaml:"max_age_days" env:"RWS_EVENTS_MAX_AGE_DAYS"`
}

// New builds the configured publisher.
func New(ctx context.Context, cfg Config, log *slog.Logger) (Publisher, error) {
	switch cfg.Driver {
	case "", "none":
		return nop{}, nil
	case "log":
		return logPub{log: log}, nil
	case "nats":
		return newNATS(ctx, cfg, log)
	default:
		return nil, fmt.Errorf("events: unknown driver %q", cfg.Driver)
	}
}

type nop struct{}

func (nop) Publish(context.Context, string, string, any) error { return nil }
func (nop) Check(context.Context) error                        { return nil }
func (nop) Close() error                                       { return nil }

type logPub struct{ log *slog.Logger }

func (l logPub) Publish(_ context.Context, subject, msgID string, payload any) error {
	l.log.Info("event", "subject", subject, "msg_id", msgID, "payload", payload)
	return nil
}
func (logPub) Check(context.Context) error { return nil }
func (logPub) Close() error                { return nil }

type natsPub struct {
	nc *nats.Conn
	js jetstream.JetStream
}

func newNATS(ctx context.Context, cfg Config, log *slog.Logger) (*natsPub, error) {
	if cfg.URL == "" {
		cfg.URL = nats.DefaultURL
	}
	if cfg.Stream == "" {
		cfg.Stream = "RWS_DICOM"
	}
	if cfg.Replicas <= 0 {
		cfg.Replicas = 1
	}
	if cfg.MaxAgeDays <= 0 {
		cfg.MaxAgeDays = 7
	}
	nc, err := nats.Connect(cfg.URL,
		nats.Name("rws-dicom"),
		nats.MaxReconnects(-1), // keep trying forever; readiness reports the outage
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) { log.Warn("nats disconnected", "err", err) }),
		nats.ReconnectHandler(func(c *nats.Conn) { log.Info("nats reconnected", "url", c.ConnectedUrl()) }),
	)
	if err != nil {
		return nil, fmt.Errorf("events: connect nats: %w", err)
	}
	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()
		return nil, err
	}
	_, err = js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:       cfg.Stream,
		Subjects:   []string{"rws.dicom.>"},
		Storage:    jetstream.FileStorage,
		Replicas:   cfg.Replicas,
		MaxAge:     time.Duration(cfg.MaxAgeDays) * 24 * time.Hour,
		Duplicates: 10 * time.Minute, // de-duplicates retried publishes by msg id
	})
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("events: ensure stream: %w", err)
	}
	return &natsPub{nc: nc, js: js}, nil
}

func (n *natsPub) Publish(ctx context.Context, subject, msgID string, payload any) error {
	b, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = n.js.Publish(ctx, subject, b, jetstream.WithMsgID(msgID))
	return err
}

func (n *natsPub) Check(_ context.Context) error {
	if !n.nc.IsConnected() {
		return fmt.Errorf("nats not connected (status %s)", n.nc.Status())
	}
	return nil
}

func (n *natsPub) Close() error {
	return n.nc.Drain()
}
