package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/dcm"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/events"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
)

// blobKey is the storage layout: one folder per study, one per series.
func blobKey(study, series, sop string) string {
	return study + "/" + series + "/" + sop + ".dcm"
}

func asConflict(err error) (index.Conflict, bool) {
	var ce *index.ConflictError
	if errors.As(err, &ce) {
		return ce.Conflict, true
	}
	return index.Conflict{}, false
}

// lateConflict handles a conflict that appeared between the pre-check and the
// index transaction (another node indexed the study first). The file already
// written under the normal key belongs to no index row; it is replaced by a
// held copy. If the SOP Instance UID was already archived under that same key
// it has been overwritten: this needs two nodes to receive the same SOP
// instance for different patients within milliseconds (residual risk,
// risk-management.md HAZ-013).
func (g *Gateway) lateConflict(ctx context.Context, req ae.StoreRequest, rec index.Record, c index.Conflict, log *slog.Logger) (uint16, string) {
	log.Warn("conflict detected inside the index transaction", "study", rec.StudyUID)
	return g.storeConflict(ctx, req, rec, c, log)
}

// onCStore archives the instance exactly as received, indexes it, then
// publishes an event. The order matters: the instance is durable on disk
// before it is visible to queries, and visible to queries before anyone is
// told it exists.
func (g *Gateway) onCStore(ctx context.Context, req ae.StoreRequest) uint16 {
	start := time.Now()
	result := func(code uint16, label string) uint16 {
		g.m.stores.WithLabelValues(label).Inc()
		g.m.storeSeconds.Observe(time.Since(start).Seconds())
		return code
	}
	log := g.log.With("sop_instance_uid", req.AffectedSOPInstanceUID)

	if len(req.Dataset) == 0 || req.TransferSyntax == "" {
		log.Warn("C-STORE without dataset")
		return result(status.CannotUnderstand, "invalid")
	}
	if req.Data == nil {
		log.Warn("C-STORE dataset could not be decoded", "transfer_syntax", req.TransferSyntax)
		return result(status.CannotUnderstand, "invalid")
	}

	inst := dcm.ExtractInstance(req.Data)
	if inst.SOPInstanceUID == "" {
		inst.SOPInstanceUID = req.AffectedSOPInstanceUID
	}
	if inst.SOPClassUID == "" {
		inst.SOPClassUID = req.AffectedSOPClassUID
	}
	if inst.SOPInstanceUID != req.AffectedSOPInstanceUID || inst.StudyInstanceUID == "" || inst.SeriesInstanceUID == "" {
		log.Warn("C-STORE dataset missing or mismatched identifiers",
			"study", inst.StudyInstanceUID, "series", inst.SeriesInstanceUID, "dataset_sop", inst.SOPInstanceUID)
		return result(status.DataSetDoesNotMatchSOPClassError, "invalid")
	}

	var sourceAE string
	if a, ok := ae.AssociationFromContext(ctx); ok {
		sourceAE = a.CallingAE
	}
	rec := index.Record{
		PatientID: inst.PatientID, PatientName: inst.PatientName,
		PatientBirthDate: inst.PatientBirthDate, PatientSex: inst.PatientSex,
		StudyUID: inst.StudyInstanceUID, StudyDate: inst.StudyDate, StudyTime: inst.StudyTime,
		AccessionNumber: inst.AccessionNumber, StudyID: inst.StudyID,
		StudyDescription: inst.StudyDescription, ReferringPhysician: inst.ReferringPhysicianName,
		SeriesUID: inst.SeriesInstanceUID, Modality: inst.Modality,
		SeriesNumber: inst.SeriesNumber, SeriesDescription: inst.SeriesDescription,
		SOPInstanceUID: inst.SOPInstanceUID, SOPClassUID: inst.SOPClassUID,
		InstanceNumber: inst.InstanceNumber, TransferSyntaxUID: req.TransferSyntax,
		SourceAE: sourceAE,
	}

	// Patient association check before the file is written, so a conflicting
	// instance can never overwrite an archived file (DCM-SRS-070, -072).
	chk, err := g.idx.CheckConflict(ctx, rec)
	if err != nil {
		log.Error("conflict check", "err", err)
		return result(status.OutOfResources, "error")
	}
	if chk.Conflict != nil {
		return result(g.storeConflict(ctx, req, rec, *chk.Conflict, log))
	}
	if chk.DemographicsMismatch {
		g.cm.mismatches.Inc()
		log.Warn("patient demographics differ from the first registration; first registration kept",
			"study", inst.StudyInstanceUID, "patient_id", g.auditPatientID(inst.PatientID))
	}

	file, err := dcm.Part10(req.AffectedSOPClassUID, req.AffectedSOPInstanceUID, req.TransferSyntax, "", req.Dataset)
	if err != nil {
		log.Error("build Part 10 file", "err", err)
		return result(status.OutOfResources, "error")
	}

	key := blobKey(inst.StudyInstanceUID, inst.SeriesInstanceUID, inst.SOPInstanceUID)
	n, err := g.blobs.Put(ctx, key, bytes.NewReader(file))
	if err != nil {
		log.Error("archive instance", "key", key, "err", err)
		return result(status.OutOfResources, "error")
	}

	receivedAt := time.Now().UTC()
	rec.BlobKey, rec.SizeBytes, rec.ReceivedAt = key, n, receivedAt
	ev := events.InstanceStored{
		EventID:    inst.SOPInstanceUID + "@" + receivedAt.Format(time.RFC3339Nano),
		OccurredAt: receivedAt, Node: g.node, SourceAE: sourceAE,
		PatientID: inst.PatientID, StudyUID: inst.StudyInstanceUID, SeriesUID: inst.SeriesInstanceUID,
		SOPInstanceUID: inst.SOPInstanceUID, SOPClassUID: inst.SOPClassUID, Modality: inst.Modality,
		AccessionNum: inst.AccessionNumber, TransferSyntax: req.TransferSyntax, BlobKey: key, SizeBytes: n,
	}
	if g.cfg.EventsOmitPatientID {
		ev.PatientID = ""
	}

	if g.outbox != nil {
		// Instance and event are committed together; the relay publishes the
		// event, retrying through bus outages, so no event is lost.
		payload, err := json.Marshal(ev)
		if err != nil {
			log.Error("encode event", "err", err)
			return result(status.OutOfResources, "error")
		}
		if err := g.idx.UpsertWithEvent(ctx, rec, &index.OutboxEvent{
			MsgID: ev.EventID, Subject: events.SubjectInstanceStored, Payload: payload,
		}); err != nil {
			if c, ok := asConflict(err); ok {
				return result(g.lateConflict(ctx, req, rec, c, log))
			}
			log.Error("index instance", "err", err)
			return result(status.OutOfResources, "error")
		}
		g.outbox.Nudge()
	} else {
		if err := g.idx.Upsert(ctx, rec); err != nil {
			if c, ok := asConflict(err); ok {
				return result(g.lateConflict(ctx, req, rec, c, log))
			}
			log.Error("index instance", "err", err)
			return result(status.OutOfResources, "error")
		}
		pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		defer cancel()
		if err := g.events.Publish(pctx, events.SubjectInstanceStored, ev.EventID, ev); err != nil {
			// The instance is safely archived and indexed; a missed event must
			// not make the modality resend. Enable the outbox to avoid losing it.
			g.m.eventFailures.Inc()
			log.Warn("publish instance.stored failed", "err", err)
		}
	}

	g.audit(ctx, auditInstancesStored, "success", "study", inst.StudyInstanceUID,
		"patient_id", g.auditPatientID(inst.PatientID), "sop_instance_uid", inst.SOPInstanceUID)
	g.m.storeBytes.Add(float64(n))
	log.Debug("instance stored", "study", inst.StudyInstanceUID, "bytes", n, "ms", time.Since(start).Milliseconds())
	return result(status.Success, "success")
}
