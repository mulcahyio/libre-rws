package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sort"
	"strconv"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/dimse"
	"github.com/godicom-dev/gonetdicom/status"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/dcm"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
)

// Design note — why C-MOVE does not use gonetdicom's MovePlan.Stores:
//
// The library's built-in C-MOVE sub-operation path (v0.17) takes every
// instance up front as an in-memory slice and proposes only Implicit VR Little
// Endian to the destination. That would hold a whole study in RAM and break
// compressed images. Instead we open the destination association ourselves,
// propose each instance's stored transfer syntax, and stream one instance at a
// time from storage. The trade-off: no Pending progress responses (optional in
// PS3.4) and C-CANCEL is not honoured mid-transfer. Fixing that belongs in the
// library (a streaming plan iterator); see README "gonetdicom fork items".

const maxContextsPerAssoc = 120 // < 128 odd presentation-context IDs

// errUniversalRetrieve guards against "retrieve everything" requests.
var errUniversalRetrieve = errors.New("retrieve identifier has no unique keys")

func (g *Gateway) resolveRetrieve(ctx context.Context, ident *godicom.Dataset, model string) ([]index.Row, uint16) {
	q, err := parseIdentifier(ident, model, g.queryOptions())
	if err != nil {
		return nil, status.IdentifierDoesNotMatchSOPClass
	}
	hasKey := false
	for _, f := range q.filters {
		switch f.Column {
		case "st.patient_id", "st.study_uid", "se.series_uid", "i.sop_instance_uid":
			hasKey = true
		}
	}
	if !hasKey {
		g.log.Warn("refusing retrieve without unique keys", "err", errUniversalRetrieve)
		return nil, status.IdentifierDoesNotMatchSOPClass
	}
	rows, err := g.idx.Find(ctx, index.Image, q.filters, 0)
	if err != nil {
		g.log.Error("retrieve lookup failed", "err", err)
		return nil, status.UnableToCalculateNumberOfMatches
	}
	return rows, 0
}

// readDataset loads a stored instance and returns the dataset bytes (no meta).
func (g *Gateway) readDataset(ctx context.Context, key string) ([]byte, error) {
	rc, err := g.blobs.Get(ctx, key)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	file, err := io.ReadAll(rc)
	if err != nil {
		return nil, err
	}
	off, err := dcm.DatasetOffset(file)
	if err != nil {
		return nil, err
	}
	return file[off:], nil
}

type subOpTally struct {
	completed, failed, warning int
	failedUIDs                 []string
}

func (t *subOpTally) fail(uid string) {
	t.failed++
	t.failedUIDs = append(t.failedUIDs, uid)
}

// final builds the closing C-MOVE/C-GET response.
func (t subOpTally) final() ae.RetrieveMatch {
	m := ae.RetrieveMatch{
		Status: status.Success,
		SubOperations: dimse.SubOperations{
			Completed: clamp16(t.completed), Failed: clamp16(t.failed),
			Warning: clamp16(t.warning), Present: true,
		},
	}
	if t.failed > 0 || t.warning > 0 {
		m.Status = status.OneOrMoreFailures
	}
	if t.failed > 0 && t.completed == 0 && t.warning == 0 {
		m.Status = status.UnableToPerformSubOperations
	}
	if len(t.failedUIDs) > 0 {
		ds := godicom.NewDataset()
		_ = ds.SetStrings(godicom.MustTag("FailedSOPInstanceUIDList"), t.failedUIDs...)
		m.Identifier = ds
	}
	return m
}

func clamp16(n int) uint16 {
	if n > 65535 {
		return 65535
	}
	return uint16(n)
}

func isWarning(code uint16) bool { return code == 0x0001 || code>>12 == 0xB }

// ---------------------------------------------------------------- C-MOVE

func (g *Gateway) onCMove(ctx context.Context, req ae.MoveRequest) ae.MovePlan {
	dest, ok := g.remoteAE(req.MoveDestination)
	if !ok {
		g.log.Warn("C-MOVE to unknown destination", "destination", req.MoveDestination)
		return ae.MovePlan{Responses: []ae.RetrieveMatch{{Status: status.MoveDestinationUnknown}}}
	}
	rows, code := g.resolveRetrieve(ctx, req.IdentifierData, req.QueryModel)
	if code != 0 {
		return ae.MovePlan{Responses: []ae.RetrieveMatch{{Status: code}}}
	}
	start := time.Now()
	tally := g.sendInstances(ctx, dest, rows)
	studies, patients := studiesAndPatients(rows)
	outcome := "success"
	if tally.failed > 0 {
		outcome = "minor-failure"
	}
	g.audit(ctx, auditInstancesExported, outcome, "op", "C-MOVE", "destination", req.MoveDestination,
		"destination_host", dest.Host, "studies", studies, "patient_ids", g.auditPatientIDs(patients),
		"instances", len(rows), "failed", tally.failed)
	g.log.Info("C-MOVE finished", "destination", req.MoveDestination, "instances", len(rows),
		"completed", tally.completed, "failed", tally.failed, "warning", tally.warning,
		"seconds", time.Since(start).Seconds())
	return ae.MovePlan{Responses: []ae.RetrieveMatch{tally.final()}}
}

// sendInstances pushes rows to dest, grouped so each association proposes
// exactly the transfer syntax every instance is stored in.
func (g *Gateway) sendInstances(ctx context.Context, dest RemoteAE, rows []index.Row) subOpTally {
	var tally subOpTally
	groups := map[string][]index.Row{} // "native" or a compressed TS UID
	for _, r := range rows {
		k := r["_TransferSyntaxUID"]
		if dcm.IsUncompressed(k) {
			k = "native"
		}
		groups[k] = append(groups[k], r)
	}
	keys := make([]string, 0, len(groups))
	for k := range groups {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		for _, chunk := range chunkByClass(groups[k]) {
			g.sendChunk(ctx, dest, k, chunk, &tally)
		}
	}
	return tally
}

// chunkByClass splits rows so no association needs more than
// maxContextsPerAssoc presentation contexts.
func chunkByClass(rows []index.Row) [][]index.Row {
	var out [][]index.Row
	var cur []index.Row
	classes := map[string]bool{}
	for _, r := range rows {
		c := r["SOPClassUID"]
		if !classes[c] && len(classes) == maxContextsPerAssoc {
			out = append(out, cur)
			cur, classes = nil, map[string]bool{}
		}
		classes[c] = true
		cur = append(cur, r)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func (g *Gateway) sendChunk(ctx context.Context, dest RemoteAE, group string, rows []index.Row, tally *subOpTally) {
	tss := []string{group}
	if group == "native" {
		tss = []string{dcm.ExplicitVRLittleEndian, dcm.ImplicitVRLittleEndian}
	}
	seen := map[string]bool{}
	var pcs []ae.PresentationContext
	for _, r := range rows {
		if c := r["SOPClassUID"]; !seen[c] {
			seen[c] = true
			pcs = append(pcs, ae.PresentationContext{AbstractSyntax: c, TransferSyntaxes: tss})
		}
	}

	addr := net.JoinHostPort(dest.Host, strconv.Itoa(dest.Port))
	assoc, err := ae.Dial(ctx, ae.Config{
		AETitle:              g.cfg.AETitle,
		MaxPDULength:         g.cfg.MaxPDULength,
		DialTimeout:          10 * time.Second,
		IdleTimeout:          g.cfg.IdleTimeout,
		PresentationContexts: pcs,
		Logger:               g.log,
	}, addr, dest.AETitle)
	if err != nil {
		g.log.Error("retrieve: association to destination failed", "addr", addr, "called_ae", dest.AETitle, "err", err)
		for _, r := range rows {
			tally.fail(r["SOPInstanceUID"])
			g.m.retrieveSubOps.WithLabelValues("move", "failed").Inc()
		}
		return
	}
	defer func() {
		rctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := assoc.Release(rctx); err != nil {
			_ = assoc.Abort()
		}
	}()

	accepted := map[string]string{}
	for _, c := range assoc.Contexts() {
		accepted[c.AbstractSyntax] = c.TransferSyntax
	}

	for _, r := range rows {
		uid := r["SOPInstanceUID"]
		if err := ctx.Err(); err != nil {
			tally.fail(uid)
			continue
		}
		res, err := g.storeOne(ctx, assoc, r, accepted)
		switch {
		case err != nil:
			g.log.Warn("retrieve sub-operation failed", "sop_instance_uid", uid, "err", err)
			tally.fail(uid)
			g.m.retrieveSubOps.WithLabelValues("move", "failed").Inc()
		case res.Status == status.Success:
			tally.completed++
			g.m.retrieveSubOps.WithLabelValues("move", "completed").Inc()
		case isWarning(res.Status):
			tally.warning++
			g.m.retrieveSubOps.WithLabelValues("move", "warning").Inc()
		default:
			g.log.Warn("destination rejected instance", "sop_instance_uid", uid, "status", fmt.Sprintf("0x%04X", res.Status))
			tally.fail(uid)
			g.m.retrieveSubOps.WithLabelValues("move", "failed").Inc()
		}
	}
}

// storeOne sends a single instance. Only this instance is held in memory.
func (g *Gateway) storeOne(ctx context.Context, assoc *ae.Association, r index.Row, accepted map[string]string) (*ae.StoreResult, error) {
	class, stored := r["SOPClassUID"], r["_TransferSyntaxUID"]
	negotiated, ok := accepted[class]
	if !ok {
		return nil, fmt.Errorf("destination accepted no presentation context for %s", class)
	}
	raw, err := g.readDataset(ctx, r["_BlobKey"])
	if err != nil {
		return nil, fmt.Errorf("read archive: %w", err)
	}
	req := ae.StoreRequest{AffectedSOPClassUID: class, AffectedSOPInstanceUID: r["SOPInstanceUID"]}
	switch {
	case negotiated == stored:
		req.Dataset, req.TransferSyntax = raw, stored // byte-for-byte, no re-encode
	case dcm.IsUncompressed(negotiated) && dcm.IsUncompressed(stored):
		ds, err := godicom.DecodeDataset(raw, godicom.UID(stored))
		if err != nil {
			return nil, fmt.Errorf("decode for re-encode: %w", err)
		}
		req.Data = ds // library encodes into the negotiated syntax
	default:
		return nil, fmt.Errorf("no compatible transfer syntax (stored %s, negotiated %s)", stored, negotiated)
	}
	return assoc.CStore(ctx, req)
}

// ---------------------------------------------------------------- C-GET

// onCGet answers C-GET. gonetdicom v0.17 sends C-GET sub-operations from an
// in-memory plan and picks the requestor's first accepted transfer syntax per
// SOP class without telling the handler which one it chose. So this handler
// (a) caps the bytes it will buffer (get_max_bytes) and (b) serves only
// natively encoded instances, which the library can re-encode safely. Compressed
// instances are reported as failed sub-operations; use C-MOVE (or DICOMweb,
// next module) for those until the fork item lands.
func (g *Gateway) onCGet(ctx context.Context, req ae.GetRequest) ae.GetPlan {
	rows, code := g.resolveRetrieve(ctx, req.IdentifierData, req.QueryModel)
	if code != 0 {
		return ae.GetPlan{Responses: []ae.RetrieveMatch{{Status: code}}}
	}
	var (
		tally  subOpTally
		stores []ae.StoreRequest
		total  int64
	)
	for _, r := range rows {
		uid := r["SOPInstanceUID"]
		if !dcm.IsUncompressed(r["_TransferSyntaxUID"]) {
			tally.fail(uid)
			g.m.retrieveSubOps.WithLabelValues("get", "failed").Inc()
			continue
		}
		raw, err := g.readDataset(ctx, r["_BlobKey"])
		if err != nil {
			tally.fail(uid)
			g.m.retrieveSubOps.WithLabelValues("get", "failed").Inc()
			continue
		}
		total += int64(len(raw))
		if g.cfg.GetMaxBytes > 0 && total > g.cfg.GetMaxBytes {
			g.log.Warn("C-GET exceeds get_max_bytes; refusing", "limit", g.cfg.GetMaxBytes)
			return ae.GetPlan{Responses: []ae.RetrieveMatch{{Status: status.UnableToPerformSubOperations}}}
		}
		ds, err := godicom.DecodeDataset(raw, godicom.UID(r["_TransferSyntaxUID"]))
		if err != nil {
			tally.fail(uid)
			continue
		}
		stores = append(stores, ae.StoreRequest{
			AffectedSOPClassUID: r["SOPClassUID"], AffectedSOPInstanceUID: uid, Data: ds,
		})
	}
	// The library does not report per-store results back for C-GET, so
	// Completed counts what was sent.
	tally.completed = len(stores)
	g.m.retrieveSubOps.WithLabelValues("get", "completed").Add(float64(len(stores)))
	studies, patients := studiesAndPatients(rows)
	outcome := "success"
	if tally.failed > 0 {
		outcome = "minor-failure"
	}
	g.audit(ctx, auditInstancesExported, outcome, "op", "C-GET", "studies", studies, "patient_ids", g.auditPatientIDs(patients),
		"instances", len(stores), "failed", tally.failed)
	return ae.GetPlan{Stores: stores, Responses: []ae.RetrieveMatch{tally.final()}}
}
