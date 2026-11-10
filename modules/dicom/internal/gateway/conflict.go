package gateway

// Patient association integrity (DCM-SRS-070…079, HAZ-013): what C-STORE
// does with an instance that contradicts the indexed patient, study or series
// association, and how an administrator resolves it.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/dcm"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
)

// Conflict policies (patient_conflict_policy).
const (
	PolicyQuarantine     = "quarantine"      // default: store, hold, wait for an administrator
	PolicyReject         = "reject"          // refuse with StatusConflictingPatientID
	PolicyAcceptSeparate = "accept_separate" // store as a separate, visible patient/study entry
)

// Visibility of held instances (patient_conflict_visibility).
const (
	VisibilityHidden  = "hidden"  // default: not returned by C-FIND, C-MOVE, C-GET
	VisibilityFlagged = "flagged" // returned under the incoming (or coerced) Patient ID
)

// CoercePrefix is prepended to the Patient ID of a conflicting instance when
// patient_conflict_coerce_patient_id is on (decision 2026-11-10: "B000_",
// after the C-STORE warning status it causes).
const CoercePrefix = "B000_"

// StatusConflictingPatientID refuses a conflicting instance under the reject
// policy. A7xx ("Refused: Out of Resources" class) tells most senders to keep
// the instance queued and retry, so nothing is lost while the source is
// corrected; the code matches dcm4chee-arc's "Conflicting Patient ID" (A778).
const StatusConflictingPatientID uint16 = 0xA778

// StatusCoercion is the C-STORE Warning "Coercion of Data Elements" (PS3.4 B.2.3).
const StatusCoercion uint16 = 0xB000

const (
	auditConflictDetected = "PatientRecord.conflict-detected"
	auditConflictResolved = "PatientRecord.conflict-resolved"
	modifyingSystemStore  = "LibreDCM"
	modifyingSystemAdmin  = "LibreDCM (administrator correction)"
)

func (g *Gateway) conflictPolicy() string {
	switch g.cfg.PatientConflictPolicy {
	case PolicyReject, PolicyAcceptSeparate:
		return g.cfg.PatientConflictPolicy
	default:
		return PolicyQuarantine
	}
}

func (g *Gateway) flagged() bool { return g.cfg.PatientConflictVisibility == VisibilityFlagged }

// findOptions: accepted separate studies are always visible; held ones only
// when visibility is flagged.
func (g *Gateway) findOptions() index.FindOptions {
	return index.FindOptions{Accepted: true, Open: g.flagged()}
}

// validateConflictConfig logs invalid values; unknown values fall back to the
// safest behaviour (quarantine, hidden) instead of failing open.
func (g *Gateway) validateConflictConfig() {
	switch g.cfg.PatientConflictPolicy {
	case "", PolicyQuarantine, PolicyReject, PolicyAcceptSeparate:
	default:
		g.log.Error("invalid patient_conflict_policy; using quarantine", "value", g.cfg.PatientConflictPolicy)
	}
	switch g.cfg.PatientConflictVisibility {
	case "", VisibilityHidden, VisibilityFlagged:
	default:
		g.log.Error("invalid patient_conflict_visibility; using hidden", "value", g.cfg.PatientConflictVisibility)
	}
	if g.flagged() && !g.cfg.PatientConflictCoercePatientID {
		g.log.Warn("held conflict instances are visible (flagged) without Patient ID coercion: they appear under the incoming Patient ID with no visible marker")
	}
}

type conflictMetrics struct {
	conflicts    *prometheus.CounterVec
	mismatches   prometheus.Counter
	coercions    prometheus.Counter
	resolutions  *prometheus.CounterVec
	openConflict prometheus.GaugeFunc
}

func (g *Gateway) registerConflictMetrics(reg prometheus.Registerer) {
	m := &conflictMetrics{
		conflicts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "libre_dcm_patient_conflicts_total",
			Help: "Instances whose patient, study or series association conflicted with the index, by kind and policy applied.",
		}, []string{"kind", "policy"}),
		mismatches: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "libre_dcm_patient_demographics_mismatch_total",
			Help: "Instances with a known Patient ID but a different name, birth date or sex (first registration kept).",
		}),
		coercions: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "libre_dcm_patient_id_coercions_total",
			Help: "Conflicting instances stored with a coerced (B000_) Patient ID.",
		}),
		resolutions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "libre_dcm_patient_conflict_resolutions_total", Help: "Conflicts resolved by an administrator, by action.",
		}, []string{"action"}),
	}
	m.openConflict = prometheus.NewGaugeFunc(prometheus.GaugeOpts{
		Name: "libre_dcm_patient_conflicts_open",
		Help: "Conflicts held in quarantine waiting for an administrator (shared index: same value on every node).",
	}, func() float64 {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		n, err := g.idx.CountOpenConflicts(ctx)
		if err != nil {
			return -1
		}
		return float64(n)
	})
	reg.MustRegister(m.conflicts, m.mismatches, m.coercions, m.resolutions, m.openConflict)
	g.cm = m
}

func conflictKey(study, sop string, at time.Time) string {
	return "conflicts/" + study + "/" + sop + "-" + strconv.FormatInt(at.UnixNano(), 36) + ".dcm"
}

// storeConflict handles an instance that conflicts with the index. The file
// is stored under its own key so it can never overwrite an archived instance.
func (g *Gateway) storeConflict(ctx context.Context, req ae.StoreRequest, rec index.Record, c index.Conflict, log *slog.Logger) (uint16, string) {
	policy := g.conflictPolicy()
	g.cm.conflicts.WithLabelValues(string(c.Kind), policy).Inc()
	auditBase := []any{"kind", string(c.Kind), "policy", policy, "study", c.StudyUID, "series", c.SeriesUID,
		"sop_instance_uid", c.SOPInstanceUID, "existing_study", c.ExistingStudyUID,
		"existing_patient_id", g.auditPatientID(c.ExistingPatientID), "incoming_patient_id", g.auditPatientID(c.IncomingPatientID)}
	log.Warn("patient association conflict", append([]any{"policy", policy, "kind", string(c.Kind), "study", c.StudyUID}, "existing_study", c.ExistingStudyUID)...)

	if policy == PolicyReject {
		g.audit(ctx, auditConflictDetected, "minor-failure", append(auditBase, "action", "rejected")...)
		return StatusConflictingPatientID, "conflict-rejected"
	}

	dataset := req.Dataset
	code := status.Success
	coerced := false
	if g.cfg.PatientConflictCoercePatientID && c.Kind == index.ConflictPatientID {
		newID := CoercePrefix + rec.PatientID
		explicit, ok := dcm.IsExplicitLE(req.TransferSyntax)
		switch {
		case len(newID) > dcm.MaxLO:
			log.Warn("Patient ID not coerced: would exceed 64 characters", "study", c.StudyUID)
		case !ok:
			log.Warn("Patient ID not coerced: transfer syntax not supported", "transfer_syntax", req.TransferSyntax)
		default:
			out, changed, err := dcm.Coerce(req.Dataset, explicit,
				[]dcm.Change{{Tag: dcm.TagPatientID, VR: "LO", Value: newID}}, dcm.ReasonCoerce, modifyingSystemStore, time.Now())
			if err != nil {
				log.Warn("Patient ID not coerced", "err", err)
			} else if changed {
				dataset, rec.PatientID, coerced, code = out, newID, true, StatusCoercion
				g.cm.coercions.Inc()
			}
		}
	}

	file, err := dcm.Part10(req.AffectedSOPClassUID, req.AffectedSOPInstanceUID, req.TransferSyntax, "", dataset)
	if err != nil {
		log.Error("build Part 10 file", "err", err)
		return status.OutOfResources, "error"
	}
	receivedAt := time.Now().UTC()
	key := conflictKey(c.StudyUID, c.SOPInstanceUID, receivedAt)
	n, err := g.blobs.Put(ctx, key, bytes.NewReader(file))
	if err != nil {
		log.Error("archive held instance", "key", key, "err", err)
		return status.OutOfResources, "error"
	}
	rec.BlobKey, rec.SizeBytes, rec.ReceivedAt = key, n, receivedAt
	state := index.StateOpen
	if policy == PolicyAcceptSeparate {
		state = index.StateAccepted
	}
	id, err := g.idx.RecordConflict(ctx, c, rec, state, coerced)
	if err != nil {
		log.Error("record conflict", "err", err)
		return status.OutOfResources, "error"
	}
	g.audit(ctx, auditConflictDetected, "minor-failure", append(auditBase, "action", state, "conflict_id", id,
		"coerced", coerced, "stored_patient_id", g.auditPatientID(rec.PatientID))...)
	g.m.storeBytes.Add(float64(n))
	label := "conflict-" + state
	if coerced {
		label += "-coerced"
	}
	return code, label
}

// ---------------------------------------------------------------- resolve

// Resolve actions (DCM-SRS-077…079).
const (
	ActionCorrectIncoming = "correct_incoming" // incoming instances belong to the indexed patient
	ActionCorrectExisting = "correct_existing" // the indexed study was wrong; the incoming patient is right
	ActionReject          = "reject"           // reject the incoming instances (kept, hidden)
)

// ErrInvalidRequest wraps resolve requests that are refused before any change.
var ErrInvalidRequest = errors.New("invalid request")

// AttrChange is one planned attribute change, for the confirmation dialog.
type AttrChange struct {
	Attribute string `json:"attribute"`
	From      string `json:"from"`
	To        string `json:"to"`
}

// ResolvePlan says exactly what an action would change.
type ResolvePlan struct {
	ConflictID        string       `json:"conflict_id"`
	Action            string       `json:"action"`
	Summary           string       `json:"summary"`
	ExistingInstances int          `json:"existing_instances_rewritten"`
	IncomingInstances int          `json:"incoming_instances_rewritten"`
	ExistingChanges   []AttrChange `json:"existing_changes,omitempty"`
	IncomingChanges   []AttrChange `json:"incoming_changes,omitempty"`
	Warnings          []string     `json:"warnings,omitempty"`
}

// ResolveResult reports what was done.
type ResolveResult struct {
	ResolvePlan
	Rewritten int    `json:"files_rewritten"`
	State     string `json:"state"`
}

func diffDemographics(from, to index.Demographics) []AttrChange {
	var out []AttrChange
	add := func(name, a, b string) {
		if a != b {
			out = append(out, AttrChange{Attribute: name, From: a, To: b})
		}
	}
	add("PatientID", from.PatientID, to.PatientID)
	add("PatientName", from.PatientName, to.PatientName)
	add("PatientBirthDate", from.PatientBirthDate, to.PatientBirthDate)
	add("PatientSex", from.PatientSex, to.PatientSex)
	return out
}

func incomingDemographics(ci index.ConflictInfo) index.Demographics {
	return index.Demographics{PatientID: ci.IncomingPatientID, PatientName: ci.PatientName,
		PatientBirthDate: ci.PatientBirthDate, PatientSex: ci.PatientSex}
}

// PlanResolve describes an action without changing anything; Libre WT shows
// it in the warning dialog before the administrator approves the edit.
func (g *Gateway) PlanResolve(ctx context.Context, id, action string) (ResolvePlan, error) {
	ci, err := g.idx.GetConflict(ctx, id)
	if err != nil {
		return ResolvePlan{}, err
	}
	p := ResolvePlan{ConflictID: id, Action: action}
	if ci.State != index.StateOpen && ci.State != index.StateAccepted {
		return p, fmt.Errorf("%w: conflict is already %s", ErrInvalidRequest, ci.State)
	}
	insts, err := g.idx.ConflictInstances(ctx, id)
	if err != nil {
		return p, err
	}
	switch action {
	case ActionReject:
		p.Summary = fmt.Sprintf("Reject %d incoming instance(s) for study %s. They stay archived but are never returned by query or retrieve. No file is changed.", len(insts), ci.StudyUID)
		return p, nil
	case ActionCorrectIncoming, ActionCorrectExisting:
	default:
		return p, fmt.Errorf("%w: unknown action %q", ErrInvalidRequest, action)
	}
	if ci.Kind != index.ConflictPatientID {
		return p, fmt.Errorf("%w: %s conflicts can only be rejected in this version", ErrInvalidRequest, ci.Kind)
	}
	existing, ok, err := g.idx.StudyDemographics(ctx, ci.StudyUID)
	if err != nil {
		return p, err
	}
	if !ok {
		return p, fmt.Errorf("%w: study %s is no longer indexed", ErrInvalidRequest, ci.StudyUID)
	}
	incoming := incomingDemographics(ci)
	stored := incoming
	stored.PatientID = ci.DisplayPatientID
	if action == ActionCorrectIncoming {
		p.IncomingInstances = len(insts)
		p.IncomingChanges = diffDemographics(stored, existing)
		p.Summary = fmt.Sprintf("Change the patient of %d incoming instance(s) to the study's indexed patient %q and add them to study %s.",
			len(insts), existing.PatientID, ci.StudyUID)
	} else {
		refs, err := g.idx.StudyInstances(ctx, ci.StudyUID)
		if err != nil {
			return p, err
		}
		p.ExistingInstances = len(refs)
		p.IncomingInstances = len(insts)
		p.ExistingChanges = diffDemographics(existing, incoming)
		p.IncomingChanges = diffDemographics(stored, incoming)
		p.Summary = fmt.Sprintf("Change the patient of all %d archived instance(s) of study %s from %q to %q, then add the %d incoming instance(s).",
			len(refs), ci.StudyUID, existing.PatientID, incoming.PatientID, len(insts))
		p.Warnings = append(p.Warnings, "Archived images are rewritten. Viewers and archives that already received this study keep the old patient until it is sent again.")
	}
	for _, ch := range append(append([]AttrChange(nil), p.ExistingChanges...), p.IncomingChanges...) {
		if ch.Attribute == "PatientName" && !isASCII(ch.To) {
			p.Warnings = append(p.Warnings, "The new patient name has non-ASCII characters; files that don't use UTF-8 (ISO_IR 192) will be refused.")
			break
		}
	}
	return p, nil
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// Resolve executes an action. operator and reason are mandatory and recorded
// in the audit trail; Libre WT has already shown the plan and obtained the
// administrator's approval (WT-SRS). Corrections are resumable: a failure
// leaves the conflict open with the remaining instances.
func (g *Gateway) Resolve(ctx context.Context, id, action, operator, reason string) (ResolveResult, error) {
	operator, reason = strings.TrimSpace(operator), strings.TrimSpace(reason)
	if operator == "" || reason == "" {
		return ResolveResult{}, fmt.Errorf("%w: operator and reason are required", ErrInvalidRequest)
	}
	plan, err := g.PlanResolve(ctx, id, action)
	if err != nil {
		return ResolveResult{ResolvePlan: plan}, err
	}
	res := ResolveResult{ResolvePlan: plan}
	ci, err := g.idx.GetConflict(ctx, id)
	if err != nil {
		return res, err
	}
	auditOutcome := "success"
	defer func() {
		g.audit(ctx, auditConflictResolved, auditOutcome, "conflict_id", id, "action", action, "operator", operator,
			"reason", reason, "study", ci.StudyUID, "kind", string(ci.Kind), "files_rewritten", res.Rewritten,
			"existing_patient_id", g.auditPatientID(ci.ExistingPatientID),
			"incoming_patient_id", g.auditPatientID(ci.IncomingPatientID), "error", errString(err))
	}()

	switch action {
	case ActionReject:
		if err = g.idx.ResolveConflict(ctx, id, index.StateRejected, operator, action, reason); err != nil {
			auditOutcome = "serious-failure"
			return res, err
		}
		res.State = index.StateRejected
	case ActionCorrectIncoming, ActionCorrectExisting:
		var target index.Demographics
		existing, _, e := g.idx.StudyDemographics(ctx, ci.StudyUID)
		if e != nil {
			err = e
			auditOutcome = "serious-failure"
			return res, err
		}
		target = existing
		if action == ActionCorrectExisting {
			target = incomingDemographics(ci)
			refs, e := g.idx.StudyInstances(ctx, ci.StudyUID)
			if e != nil {
				err = e
				auditOutcome = "serious-failure"
				return res, err
			}
			for _, ref := range refs {
				n, _, e := g.rewrite(ctx, ref.BlobKey, ref.BlobKey, ref.TransferSyntaxUID, target)
				res.Rewritten += n
				if e != nil {
					err = fmt.Errorf("rewrite %s: %w", ref.SOPInstanceUID, e)
					auditOutcome = "serious-failure"
					return res, err
				}
			}
			if e := g.idx.SetStudyDemographics(ctx, ci.StudyUID, target); e != nil {
				err = e
				auditOutcome = "serious-failure"
				return res, err
			}
		}
		insts, e := g.idx.ConflictInstances(ctx, id)
		if e != nil {
			err = e
			auditOutcome = "serious-failure"
			return res, err
		}
		for _, inst := range insts {
			r := inst.Record
			dst := blobKey(r.StudyUID, r.SeriesUID, r.SOPInstanceUID)
			n, size, e := g.rewrite(ctx, r.BlobKey, dst, r.TransferSyntaxUID, target)
			res.Rewritten += n
			if e != nil {
				err = fmt.Errorf("rewrite %s: %w", r.SOPInstanceUID, e)
				auditOutcome = "serious-failure"
				return res, err
			}
			r.BlobKey, r.SizeBytes = dst, size
			r.PatientID, r.PatientName, r.PatientBirthDate, r.PatientSex =
				target.PatientID, target.PatientName, target.PatientBirthDate, target.PatientSex
			if e := g.idx.Upsert(ctx, r); e != nil {
				err = fmt.Errorf("index %s: %w", r.SOPInstanceUID, e)
				auditOutcome = "serious-failure"
				return res, err
			}
			if e := g.idx.RemoveConflictInstance(ctx, id, r.SOPInstanceUID); e != nil {
				err = e
				auditOutcome = "serious-failure"
				return res, err
			}
		}
		if err = g.idx.ResolveConflict(ctx, id, index.StateCorrected, operator, action, reason); err != nil {
			auditOutcome = "serious-failure"
			return res, err
		}
		res.State = index.StateCorrected
	}
	g.cm.resolutions.WithLabelValues(action).Inc()
	return res, nil
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// rewrite reads the file at src, sets the patient attributes to target
// (Original Attributes Sequence, reason CORRECT) and writes it to dst. It
// returns 1 and the new size when a file was written. Unchanged files are skipped, so a failed
// correction can be re-run.
func (g *Gateway) rewrite(ctx context.Context, src, dst, ts string, target index.Demographics) (int, int64, error) {
	explicit, ok := dcm.IsExplicitLE(ts)
	if !ok {
		return 0, 0, fmt.Errorf("transfer syntax %s not supported", ts)
	}
	rc, err := g.blobs.Get(ctx, src)
	if err != nil {
		return 0, 0, err
	}
	file, err := io.ReadAll(rc)
	_ = rc.Close()
	if err != nil {
		return 0, 0, err
	}
	off, err := dcm.DatasetOffset(file)
	if err != nil {
		return 0, 0, err
	}
	ds := file[off:]
	if !isASCII(target.PatientName) {
		cs, err := dcm.TopLevelString(ds, explicit, dcm.TagSpecificCharacterSet)
		if err != nil {
			return 0, 0, err
		}
		if cs != "ISO_IR 192" {
			return 0, 0, fmt.Errorf("non-ASCII patient name can't be written to a file with character set %q; edit it manually", cs)
		}
	}
	out, changed, err := dcm.Coerce(ds, explicit, []dcm.Change{
		{Tag: dcm.TagPatientName, VR: "PN", Value: target.PatientName},
		{Tag: dcm.TagPatientID, VR: "LO", Value: target.PatientID},
		{Tag: dcm.TagPatientBirthDate, VR: "DA", Value: target.PatientBirthDate},
		{Tag: dcm.TagPatientSex, VR: "CS", Value: target.PatientSex},
	}, dcm.ReasonCorrect, modifyingSystemAdmin, time.Now())
	if err != nil {
		return 0, 0, err
	}
	if !changed && src == dst {
		return 0, int64(len(file)), nil
	}
	var b bytes.Buffer
	b.Grow(off + len(out))
	b.Write(file[:off]) // file meta unchanged: SOP Instance UID and syntax are the same
	b.Write(out)
	n, err := g.blobs.Put(ctx, dst, bytes.NewReader(b.Bytes()))
	if err != nil {
		return 0, 0, err
	}
	return 1, n, nil
}
