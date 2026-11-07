package gateway

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"
)

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

func TestAuditRecords(t *testing.T) {
	buf := &syncBuf{}
	logSink = buf
	t.Cleanup(func() { logSink = nil })
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	addr, gw, _ := startGateway(t, ctx, nil)
	_ = gw.SetAllowedAEs([]string{"MODALITY"})

	if a, err := dialAs(ctx, addr, "NOPE"); err == nil {
		_ = a.Release(ctx)
	}
	scu, err := dialAs(ctx, addr, "MODALITY")
	if err != nil {
		t.Fatal(err)
	}
	study, series := "1.2.826.0.1.3680043.10.999.30", "1.2.826.0.1.3680043.10.999.30.1"
	res, err := scu.CStore(ctx, ae.StoreRequest{AffectedSOPClassUID: string(uid.CTImageStorage),
		AffectedSOPInstanceUID: series + ".1", Data: ctImage(study, series, series+".1", 1)})
	if err != nil || res.Status != status.Success {
		t.Fatalf("store: %v %v", err, res)
	}
	_ = scu.Release(ctx)

	log := buf.String()
	for _, want := range []string{
		"event=SecurityAlert.association-rejected", "calling_ae=NOPE",
		"event=DICOMInstancesTransferred.received", "calling_ae=MODALITY", "study=" + study, "patient_id=P001",
	} {
		if !strings.Contains(log, want) {
			t.Errorf("audit log missing %q", want)
		}
	}
}
