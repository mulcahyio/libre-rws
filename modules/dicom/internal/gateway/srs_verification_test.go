package gateway

// Verification of DCM-SRS-043 (DICOM TLS), DCM-SRS-053 (graceful shutdown)
// and DCM-SRS-054 (metrics). Added 2026-11-10 to close the last untested
// active requirements of the retrospective SRS.

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"
	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/blob"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/dcm"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/events"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
)

// testNode is a gateway with direct access to its stores and registry.
type testNode struct {
	addr  string
	gw    *Gateway
	idx   index.Index
	blobs blob.Store
	reg   *prometheus.Registry
	done  chan error // Serve's return value
}

// startNode starts a gateway on a loopback port with a filesystem store and
// a SQLite index. mod adjusts the configuration before start.
func startNode(t *testing.T, ctx context.Context, pub events.Publisher, mod func(*Config)) *testNode {
	t.Helper()
	dir := t.TempDir()
	blobs, err := blob.NewFS(filepath.Join(dir, "blobs"))
	if err != nil {
		t.Fatal(err)
	}
	idx, err := index.Open(context.Background(), "sqlite", "file:"+filepath.Join(dir, "index.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = idx.Close() })
	if pub == nil {
		pub = &recPub{}
	}
	cfg := DefaultConfig()
	cfg.AETitle = "RWS_TEST"
	if mod != nil {
		mod(&cfg)
	}
	reg := prometheus.NewRegistry()
	gw := New(cfg, "test-node", slog.New(slog.NewTextHandler(testWriter(), nil)), reg, blobs, idx, pub)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	n := &testNode{addr: ln.Addr().String(), gw: gw, idx: idx, blobs: blobs, reg: reg, done: make(chan error, 1)}
	go func() { n.done <- gw.Serve(ctx, ln) }()
	return n
}

func storeContexts() []ae.PresentationContext {
	return []ae.PresentationContext{
		{AbstractSyntax: "1.2.840.10008.1.1", TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian}},
		{AbstractSyntax: string(uid.CTImageStorage), TransferSyntaxes: []string{dcm.ExplicitVRLittleEndian}},
	}
}

// writeTestCert writes a self-signed certificate for 127.0.0.1/localhost and
// returns the file paths and a pool that trusts it.
func writeTestCert(t *testing.T) (certFile, keyFile string, pool *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "libredcm-test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:    []string{"localhost"}, IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	certFile, keyFile = filepath.Join(dir, "tls.crt"), filepath.Join(dir, "tls.key")
	if err := os.WriteFile(certFile, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(keyFile, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600); err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool = x509.NewCertPool()
	pool.AddCert(cert)
	return certFile, keyFile, pool
}

// TestDICOMListenerTLSOnly: with a certificate configured the DICOM listener
// serves associations over TLS 1.2+, refuses plain DICOM, and refuses
// clients limited to TLS 1.0/1.1.
// Verifies: DCM-SRS-043
func TestDICOMListenerTLSOnly(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	certFile, keyFile, pool := writeTestCert(t)
	n := startNode(t, ctx, nil, func(c *Config) { c.TLSCertFile, c.TLSKeyFile = certFile, keyFile })

	for _, v := range []struct {
		name string
		ver  uint16
	}{{"TLS1.2", tls.VersionTLS12}, {"TLS1.3", tls.VersionTLS13}} {
		scu, err := ae.Dial(ctx, ae.Config{AETitle: "MODALITY", PresentationContexts: storeContexts(),
			TLS: &tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: v.ver, MaxVersion: v.ver}},
			n.addr, "RWS_TEST")
		if err != nil {
			t.Fatalf("%s dial: %v", v.name, err)
		}
		if err := scu.CEcho(ctx); err != nil {
			t.Fatalf("%s C-ECHO: %v", v.name, err)
		}
		study, series := "1.2.826.0.1.3680043.10.999.43", "1.2.826.0.1.3680043.10.999.43.1"
		sop := fmt.Sprintf("%s.%d", series, v.ver)
		res, err := scu.CStore(ctx, ae.StoreRequest{AffectedSOPClassUID: string(uid.CTImageStorage),
			AffectedSOPInstanceUID: sop, Data: ctImage(study, series, sop, 1)})
		if err != nil || res.Status != status.Success {
			t.Fatalf("%s C-STORE: %v %+v", v.name, err, res)
		}
		_ = scu.Release(ctx)
	}

	// Plain DICOM on the TLS port: no association.
	dctx, dcancel := context.WithTimeout(ctx, 5*time.Second)
	if a, err := ae.Dial(dctx, ae.Config{AETitle: "MODALITY", PresentationContexts: storeContexts()}, n.addr, "RWS_TEST"); err == nil {
		_ = a.Release(dctx)
		dcancel()
		t.Fatal("plain DICOM association accepted on a TLS listener")
	}
	dcancel()

	// Clients that only speak TLS 1.0/1.1 fail the handshake.
	for _, ver := range []uint16{tls.VersionTLS10, tls.VersionTLS11} {
		c, err := tls.DialWithDialer(&net.Dialer{Timeout: 5 * time.Second}, "tcp", n.addr,
			&tls.Config{RootCAs: pool, ServerName: "127.0.0.1", MinVersion: ver, MaxVersion: ver}) //nolint:gosec // testing that old versions are refused
		if err == nil {
			_ = c.Close()
			t.Fatalf("TLS version %#x accepted", ver)
		}
	}
}

// TestGracefulShutdown: stopping the gateway while several modalities are
// sending closes the listener and all open associations promptly, and every
// instance for which a sender received a success status is archived and
// indexed (no success for an instance that was not stored).
// Verifies: DCM-SRS-053
func TestGracefulShutdown(t *testing.T) {
	testCtx, testCancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer testCancel()
	gwCtx, stopGateway := context.WithCancel(testCtx)
	n := startNode(t, gwCtx, nil, nil)

	const senders = 4
	study := "1.2.826.0.1.3680043.10.999.53"
	var (
		mu        sync.Mutex
		succeeded []string
		stored    atomic.Int64
		wg        sync.WaitGroup
		failures  atomic.Int64
	)
	for s := 0; s < senders; s++ {
		wg.Add(1)
		go func(s int) {
			defer wg.Done()
			scu, err := ae.Dial(testCtx, ae.Config{AETitle: "MODALITY", PresentationContexts: storeContexts()}, n.addr, "RWS_TEST")
			if err != nil {
				t.Errorf("sender %d dial: %v", s, err)
				return
			}
			series := fmt.Sprintf("%s.%d", study, s+1)
			for i := 1; ; i++ {
				sop := fmt.Sprintf("%s.%d", series, i)
				res, err := scu.CStore(testCtx, ae.StoreRequest{AffectedSOPClassUID: string(uid.CTImageStorage),
					AffectedSOPInstanceUID: sop, Data: ctImage(study, series, sop, i)})
				if err != nil {
					failures.Add(1)
					return // association ended by the shutdown
				}
				if res.Status == status.Success {
					mu.Lock()
					succeeded = append(succeeded, sop)
					mu.Unlock()
					stored.Add(1)
				}
			}
		}(s)
	}

	// Let traffic flow, then stop the gateway mid-transfer.
	deadline := time.Now().Add(20 * time.Second)
	for stored.Load() < 40 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if stored.Load() == 0 {
		t.Fatal("no instance stored before shutdown")
	}
	stopGateway()
	select {
	case err := <-n.done:
		if err != nil {
			t.Fatalf("Serve returned %v on shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("gateway did not stop within 5 s")
	}

	senderDone := make(chan struct{})
	go func() { wg.Wait(); close(senderDone) }()
	select {
	case <-senderDone:
	case <-time.After(10 * time.Second):
		t.Fatal("open associations were not closed by the shutdown")
	}
	if failures.Load() != senders {
		t.Fatalf("%d of %d associations ended by shutdown", failures.Load(), senders)
	}

	// No new associations after shutdown.
	dctx, dcancel := context.WithTimeout(testCtx, 3*time.Second)
	defer dcancel()
	if a, err := ae.Dial(dctx, ae.Config{AETitle: "MODALITY", PresentationContexts: storeContexts()}, n.addr, "RWS_TEST"); err == nil {
		_ = a.Release(dctx)
		t.Fatal("association accepted after shutdown")
	}

	// Every success status corresponds to an archived, indexed, readable instance.
	mu.Lock()
	defer mu.Unlock()
	for _, sop := range succeeded {
		rows, err := n.idx.Find(testCtx, index.Image, []index.Filter{{Column: "i.sop_instance_uid", Op: index.OpEq, Values: []string{sop}}}, 2)
		if err != nil || len(rows) != 1 {
			t.Fatalf("%s: success sent but index has %d rows (err %v)", sop, len(rows), err)
		}
		rc, err := n.blobs.Get(testCtx, rows[0]["_BlobKey"])
		if err != nil {
			t.Fatalf("%s: success sent but blob missing: %v", sop, err)
		}
		b, err := io.ReadAll(rc)
		_ = rc.Close()
		if err != nil {
			t.Fatal(err)
		}
		fd, err := godicom.ReadBytes(b, nil)
		if err != nil || dcm.String(fd.Dataset, "SOPInstanceUID") != sop {
			t.Fatalf("%s: stored file unreadable or wrong instance (err %v)", sop, err)
		}
	}
	t.Logf("%d instances acknowledged before shutdown, all archived and indexed", len(succeeded))
}

// downPub fails every publish while down is set.
type downPub struct{ down atomic.Bool }

func (d *downPub) Publish(context.Context, string, string, any) error {
	if d.down.Load() {
		return errors.New("bus down")
	}
	return nil
}
func (d *downPub) Check(context.Context) error { return nil }
func (d *downPub) Close() error                { return nil }

func gatherMetrics(t *testing.T, reg prometheus.Gatherer) map[string]*dto.MetricFamily {
	t.Helper()
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]*dto.MetricFamily{}
	for _, mf := range mfs {
		out[mf.GetName()] = mf
	}
	return out
}

// metricValue returns the value of the series in family name whose labels
// include want (nil: the first series).
func metricValue(t *testing.T, mfs map[string]*dto.MetricFamily, name string, want map[string]string) float64 {
	t.Helper()
	mf, ok := mfs[name]
	if !ok {
		t.Fatalf("metric %s not exposed", name)
	}
	for _, m := range mf.GetMetric() {
		match := true
		for k, v := range want {
			found := false
			for _, lp := range m.GetLabel() {
				if lp.GetName() == k && lp.GetValue() == v {
					found = true
				}
			}
			match = match && found
		}
		if !match {
			continue
		}
		switch {
		case m.Counter != nil:
			return m.GetCounter().GetValue()
		case m.Gauge != nil:
			return m.GetGauge().GetValue()
		case m.Histogram != nil:
			return float64(m.GetHistogram().GetSampleCount())
		}
	}
	t.Fatalf("metric %s has no series with labels %v", name, want)
	return 0
}

// TestMetricsExposed: associations, rejected associations, stored instances
// and pending outbox events are exposed as Prometheus metrics and follow the
// real traffic. (The licence-state gauges are verified in the licensing
// package: the gateway may not import licensing code, DCM-SRS-058.)
// Verifies: DCM-SRS-054
func TestMetricsExposed(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pub := &downPub{}
	pub.down.Store(true)
	n := startNode(t, ctx, pub, nil)
	relay := events.NewRelay(n.idx, pub, slog.New(slog.NewTextHandler(io.Discard, nil)), n.reg, n.gw.CountEventFailure)
	relay.Interval, relay.MaxDelay = 50*time.Millisecond, 100*time.Millisecond
	n.gw.UseOutbox(relay)
	go func() { _ = relay.Run(ctx) }()
	if err := n.gw.SetAllowedAEs([]string{"MODALITY"}); err != nil {
		t.Fatal(err)
	}

	// One rejected association.
	if a, err := ae.Dial(ctx, ae.Config{AETitle: "INTRUDER", PresentationContexts: storeContexts()}, n.addr, "RWS_TEST"); err == nil {
		_ = a.Release(ctx)
		t.Fatal("INTRUDER accepted")
	}
	// One association storing 3 instances while the bus is down.
	scu, err := ae.Dial(ctx, ae.Config{AETitle: "MODALITY", PresentationContexts: storeContexts()}, n.addr, "RWS_TEST")
	if err != nil {
		t.Fatal(err)
	}
	study, series := "1.2.826.0.1.3680043.10.999.54", "1.2.826.0.1.3680043.10.999.54.1"
	for i := 1; i <= 3; i++ {
		sop := fmt.Sprintf("%s.%d", series, i)
		res, err := scu.CStore(ctx, ae.StoreRequest{AffectedSOPClassUID: string(uid.CTImageStorage),
			AffectedSOPInstanceUID: sop, Data: ctImage(study, series, sop, i)})
		if err != nil || res.Status != status.Success {
			t.Fatalf("store: %v %+v", err, res)
		}
	}

	waitFor := func(what string, cond func(map[string]*dto.MetricFamily) bool) map[string]*dto.MetricFamily {
		t.Helper()
		for end := time.Now().Add(10 * time.Second); time.Now().Before(end); time.Sleep(20 * time.Millisecond) {
			if mfs := gatherMetrics(t, n.reg); cond(mfs) {
				return mfs
			}
		}
		t.Fatalf("timed out waiting for %s", what)
		return nil
	}
	has := func(mfs map[string]*dto.MetricFamily, name string) bool { _, ok := mfs[name]; return ok }

	mfs := waitFor("3 pending outbox events", func(m map[string]*dto.MetricFamily) bool {
		return has(m, "libre_dcm_outbox_pending") && metricValue(t, m, "libre_dcm_outbox_pending", nil) == 3
	})
	if got := metricValue(t, mfs, "libre_dcm_connections_total", nil); got != 2 {
		t.Errorf("connections_total = %v, want 2", got)
	}
	if got := metricValue(t, mfs, "libre_dcm_connections_active", nil); got != 1 {
		t.Errorf("connections_active = %v, want 1 (the open MODALITY association)", got)
	}
	if got := metricValue(t, mfs, "libre_dcm_associations_rejected_total", nil); got != 1 {
		t.Errorf("associations_rejected_total = %v, want 1", got)
	}
	if got := metricValue(t, mfs, "libre_dcm_cstore_total", map[string]string{"result": "success"}); got != 3 {
		t.Errorf("cstore_total{success} = %v, want 3", got)
	}
	if got := metricValue(t, mfs, "libre_dcm_cstore_bytes_total", nil); got <= 0 {
		t.Errorf("cstore_bytes_total = %v, want > 0", got)
	}
	if got := metricValue(t, mfs, "libre_dcm_event_publish_failures_total", nil); got < 1 {
		t.Errorf("event_publish_failures_total = %v, want >= 1 while the bus is down", got)
	}

	// Bus back: pending drains to 0; closing the association drops the active gauge.
	pub.down.Store(false)
	relay.Nudge()
	_ = scu.Release(ctx)
	waitFor("outbox drained and association closed", func(m map[string]*dto.MetricFamily) bool {
		return metricValue(t, m, "libre_dcm_outbox_pending", nil) == 0 &&
			metricValue(t, m, "libre_dcm_connections_active", nil) == 0
	})

	// Store latency is exposed as a histogram (Vec metrics such as C-FIND and
	// retrieve counters appear once their first request is served).
	if got := metricValue(t, gatherMetrics(t, n.reg), "libre_dcm_cstore_seconds", nil); got != 3 {
		t.Errorf("cstore_seconds sample count = %v, want 3", got)
	}
}
