package index

// Patient association integrity (DCM-SRS-070…079, risk control for HAZ-013).
//
// A conflicting instance never changes the patient, study or series an
// already indexed instance belongs to. It is recorded in conflicts /
// conflict_instances instead, where an administrator resolves it.

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"
)

// ConflictKind says which association an incoming instance contradicts.
type ConflictKind string

const (
	// ConflictPatientID: the study is indexed under another Patient ID (ANOM-002).
	ConflictPatientID ConflictKind = "patient_id"
	// ConflictSeriesStudy: the series is indexed under another study (ANOM-009).
	ConflictSeriesStudy ConflictKind = "series_study"
	// ConflictInstanceRelation: the SOP instance is indexed under another study or series.
	ConflictInstanceRelation ConflictKind = "instance_relation"
)

// Conflict states.
const (
	StateOpen      = "open"      // held, waiting for an administrator
	StateAccepted  = "accepted"  // accepted as a separate study (policy accept_separate)
	StateCorrected = "corrected" // resolved by correcting patient attributes
	StateRejected  = "rejected"  // resolved by rejecting the incoming instances
)

// Conflict describes one detected conflict.
type Conflict struct {
	Kind                                                ConflictKind
	StudyUID, SeriesUID, SOPInstanceUID                 string // incoming
	IncomingPatientID                                   string // as received
	ExistingPatientID, ExistingStudyUID, ExistingSeries string
}

// ConflictError is returned by Upsert when the record conflicts with the
// index; nothing was written.
type ConflictError struct{ Conflict Conflict }

func (e *ConflictError) Error() string {
	c := e.Conflict
	return fmt.Sprintf("index: %s conflict for study %s (indexed patient %q, incoming %q)",
		c.Kind, c.StudyUID, c.ExistingPatientID, c.IncomingPatientID)
}

// Demographics are the patient attributes kept per study.
type Demographics struct {
	PatientID, PatientName, PatientBirthDate, PatientSex string
}

// Check is the result of a read-only pre-check of a record.
type Check struct {
	Conflict *Conflict
	// DemographicsMismatch: same Patient ID, but name, birth date or sex
	// differ from the first registration (DCM-SRS-076). Existing holds the
	// indexed values.
	DemographicsMismatch bool
	Existing             Demographics
}

// FindOptions select which recorded conflicts a query may return
// (DCM-SRS-073). The zero value returns none.
type FindOptions struct {
	Open     bool // conflicts held in quarantine (visibility "flagged")
	Accepted bool // conflicts accepted as separate studies
}

func (o FindOptions) states() []string {
	var s []string
	if o.Open {
		s = append(s, StateOpen)
	}
	if o.Accepted {
		s = append(s, StateAccepted)
	}
	return s
}

// ConflictInfo is a conflict as listed for administrators.
type ConflictInfo struct {
	ID                string       `json:"id"`
	Kind              ConflictKind `json:"kind"`
	State             string       `json:"state"`
	StudyUID          string       `json:"study_uid"`
	SeriesUID         string       `json:"series_uid,omitempty"`
	ExistingPatientID string       `json:"existing_patient_id"`
	ExistingStudyUID  string       `json:"existing_study_uid,omitempty"`
	ExistingSeriesUID string       `json:"existing_series_uid,omitempty"`
	IncomingPatientID string       `json:"incoming_patient_id"`
	DisplayPatientID  string       `json:"display_patient_id"`
	PatientName       string       `json:"incoming_patient_name"`
	PatientBirthDate  string       `json:"incoming_patient_birth_date"`
	PatientSex        string       `json:"incoming_patient_sex"`
	StudyDate         string       `json:"study_date"`
	AccessionNumber   string       `json:"accession_number"`
	StudyDescription  string       `json:"study_description"`
	SourceAE          string       `json:"source_ae"`
	Coerced           bool         `json:"coerced"`
	Instances         int          `json:"instances"`
	FirstSeen         time.Time    `json:"first_seen"`
	LastSeen          time.Time    `json:"last_seen"`
	ResolvedAt        *time.Time   `json:"resolved_at,omitempty"`
	ResolvedBy        string       `json:"resolved_by,omitempty"`
	ResolutionReason  string       `json:"resolution_reason,omitempty"`
	ResolutionAction  string       `json:"resolution_action,omitempty"`
}

// ConflictInstance is one held instance.
type ConflictInstance struct {
	ConflictID        string    `json:"conflict_id"`
	Record            Record    `json:"-"`
	SOPInstanceUID    string    `json:"sop_instance_uid"`
	SeriesUID         string    `json:"series_uid"`
	Modality          string    `json:"modality"`
	SeriesNumber      string    `json:"series_number"`
	SeriesDescription string    `json:"series_description"`
	InstanceNumber    string    `json:"instance_number"`
	StoredPatientID   string    `json:"stored_patient_id"`
	ReceivedAt        time.Time `json:"received_at"`
	SourceAE          string    `json:"source_ae"`
}

// InstanceRef locates an indexed instance's file.
type InstanceRef struct {
	SOPInstanceUID, SeriesUID, TransferSyntaxUID, BlobKey string
}

// ErrNotFound is returned for unknown conflicts.
var ErrNotFound = errors.New("index: not found")

// Conflicts is implemented by SQL; the gateway uses it through Index.
type Conflicts interface {
	CheckConflict(ctx context.Context, r Record) (Check, error)
	RecordConflict(ctx context.Context, c Conflict, r Record, state string, coerced bool) (string, error)
	FindWith(ctx context.Context, level Level, filters []Filter, limit int, opts FindOptions) ([]Row, error)
	ListConflicts(ctx context.Context, filter string, limit int) ([]ConflictInfo, error)
	GetConflict(ctx context.Context, id string) (ConflictInfo, error)
	ConflictInstances(ctx context.Context, id string) ([]ConflictInstance, error)
	RemoveConflictInstance(ctx context.Context, id, sop string) error
	ResolveConflict(ctx context.Context, id, state, by, action, reason string) error
	CountOpenConflicts(ctx context.Context) (int, error)
	StudyDemographics(ctx context.Context, studyUID string) (Demographics, bool, error)
	StudyInstances(ctx context.Context, studyUID string) ([]InstanceRef, error)
	SetStudyDemographics(ctx context.Context, studyUID string, d Demographics) error
}

func newID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand doesn't fail on supported platforms
	}
	return hex.EncodeToString(b)
}

type querier interface {
	QueryRowContext(ctx context.Context, q string, args ...any) *sql.Row
}

// check runs the conflict checks against q (a tx inside Upsert, the pool for
// CheckConflict). lock adds FOR UPDATE on PostgreSQL so concurrent writers on
// other nodes serialize on the study row.
func (s *SQL) check(ctx context.Context, q querier, r Record, lock bool) (Check, error) {
	var out Check
	sel := `SELECT patient_id, patient_name, patient_birth_date, patient_sex FROM studies WHERE study_uid = ?`
	if lock && s.postgres {
		sel += " FOR UPDATE"
	}
	var d Demographics
	err := q.QueryRowContext(ctx, s.rebind(sel), r.StudyUID).Scan(&d.PatientID, &d.PatientName, &d.PatientBirthDate, &d.PatientSex)
	switch {
	case errors.Is(err, sql.ErrNoRows):
	case err != nil:
		return out, err
	default:
		out.Existing = d
		if d.PatientID != r.PatientID {
			out.Conflict = &Conflict{Kind: ConflictPatientID, StudyUID: r.StudyUID, SeriesUID: r.SeriesUID,
				SOPInstanceUID: r.SOPInstanceUID, IncomingPatientID: r.PatientID, ExistingPatientID: d.PatientID,
				ExistingStudyUID: r.StudyUID}
			return out, nil
		}
		if d.PatientName != r.PatientName || d.PatientBirthDate != r.PatientBirthDate || d.PatientSex != r.PatientSex {
			out.DemographicsMismatch = true
		}
	}
	var seriesStudy string
	err = q.QueryRowContext(ctx, s.rebind(`SELECT study_uid FROM series WHERE series_uid = ?`), r.SeriesUID).Scan(&seriesStudy)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil && seriesStudy != r.StudyUID {
		out.Conflict = &Conflict{Kind: ConflictSeriesStudy, StudyUID: r.StudyUID, SeriesUID: r.SeriesUID,
			SOPInstanceUID: r.SOPInstanceUID, IncomingPatientID: r.PatientID, ExistingStudyUID: seriesStudy,
			ExistingSeries: r.SeriesUID, ExistingPatientID: s.patientOf(ctx, q, seriesStudy)}
		return out, nil
	}
	var instStudy, instSeries string
	err = q.QueryRowContext(ctx, s.rebind(`SELECT study_uid, series_uid FROM instances WHERE sop_instance_uid = ?`), r.SOPInstanceUID).Scan(&instStudy, &instSeries)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return out, err
	}
	if err == nil && (instStudy != r.StudyUID || instSeries != r.SeriesUID) {
		out.Conflict = &Conflict{Kind: ConflictInstanceRelation, StudyUID: r.StudyUID, SeriesUID: r.SeriesUID,
			SOPInstanceUID: r.SOPInstanceUID, IncomingPatientID: r.PatientID, ExistingStudyUID: instStudy,
			ExistingSeries: instSeries, ExistingPatientID: s.patientOf(ctx, q, instStudy)}
	}
	return out, nil
}

func (s *SQL) patientOf(ctx context.Context, q querier, studyUID string) string {
	var pid string
	_ = q.QueryRowContext(ctx, s.rebind(`SELECT patient_id FROM studies WHERE study_uid = ?`), studyUID).Scan(&pid)
	return pid
}

// CheckConflict is a read-only pre-check used by the gateway to choose where
// to store the file before indexing it. Upsert repeats the check inside its
// transaction, which is authoritative.
func (s *SQL) CheckConflict(ctx context.Context, r Record) (Check, error) {
	return s.check(ctx, s.db, r, false)
}

func groupKey(c Conflict) string {
	k := string(c.Kind) + "|" + c.StudyUID + "|"
	if c.Kind != ConflictPatientID {
		k += c.SeriesUID
	}
	return k + "|" + c.IncomingPatientID
}

// RecordConflict stores a held instance. Instances of the same study arriving
// with the same conflicting Patient ID are grouped into one active conflict.
// r.PatientID is the Patient ID as stored (possibly coerced); the original is
// c.IncomingPatientID.
func (s *SQL) RecordConflict(ctx context.Context, c Conflict, r Record, state string, coerced bool) (string, error) {
	if state != StateOpen && state != StateAccepted {
		return "", fmt.Errorf("index: invalid conflict state %q", state)
	}
	if r.ReceivedAt.IsZero() {
		r.ReceivedAt = time.Now()
	}
	ms := r.ReceivedAt.UnixMilli()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer func() { _ = tx.Rollback() }()
	co := 0
	if coerced {
		co = 1
	}
	var id string
	err = tx.QueryRowContext(ctx, s.rebind(`
		INSERT INTO conflicts (id, group_key, kind, active, state, study_uid, series_uid, sop_instance_uid,
			existing_patient_id, existing_study_uid, existing_series_uid, incoming_patient_id, display_patient_id,
			patient_name, patient_name_fold, patient_birth_date, patient_sex, study_date, study_time,
			accession_number, study_id, study_description, referring_physician, source_ae, coerced,
			first_seen, last_seen)
		VALUES (?,?,?,1,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (group_key) WHERE active = 1 DO UPDATE SET last_seen = excluded.last_seen
		RETURNING id`),
		newID(), groupKey(c), string(c.Kind), state, c.StudyUID, c.SeriesUID, c.SOPInstanceUID,
		c.ExistingPatientID, c.ExistingStudyUID, c.ExistingSeries, c.IncomingPatientID, r.PatientID,
		r.PatientName, Fold(r.PatientName), r.PatientBirthDate, r.PatientSex, r.StudyDate, r.StudyTime,
		r.AccessionNumber, r.StudyID, r.StudyDescription, r.ReferringPhysician, r.SourceAE, co, ms, ms,
	).Scan(&id)
	if err != nil {
		return "", fmt.Errorf("index: record conflict: %w", err)
	}
	if _, err := tx.ExecContext(ctx, s.rebind(`
		INSERT INTO conflict_instances (conflict_id, sop_instance_uid, series_uid, study_uid, sop_class_uid,
			instance_number, transfer_syntax, blob_key, size_bytes, source_ae, received_at, modality,
			series_number, series_description, patient_id)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (conflict_id, sop_instance_uid) DO UPDATE SET
			series_uid=excluded.series_uid, sop_class_uid=excluded.sop_class_uid,
			instance_number=excluded.instance_number, transfer_syntax=excluded.transfer_syntax,
			blob_key=excluded.blob_key, size_bytes=excluded.size_bytes, source_ae=excluded.source_ae,
			received_at=excluded.received_at, modality=excluded.modality, series_number=excluded.series_number,
			series_description=excluded.series_description, patient_id=excluded.patient_id`),
		id, r.SOPInstanceUID, r.SeriesUID, r.StudyUID, r.SOPClassUID, r.InstanceNumber, r.TransferSyntaxUID,
		r.BlobKey, r.SizeBytes, r.SourceAE, ms, r.Modality, r.SeriesNumber, r.SeriesDescription, r.PatientID,
	); err != nil {
		return "", fmt.Errorf("index: record conflict instance: %w", err)
	}
	return id, tx.Commit()
}

const conflictCols = `c.id, c.kind, c.state, c.study_uid, c.series_uid, c.existing_patient_id, c.existing_study_uid,
	c.existing_series_uid, c.incoming_patient_id, c.display_patient_id, c.patient_name, c.patient_birth_date,
	c.patient_sex, c.study_date, c.accession_number, c.study_description, c.source_ae, c.coerced, c.first_seen,
	c.last_seen, c.resolved_at, c.resolved_by, c.resolution_reason, c.resolution_action,
	(SELECT COUNT(*) FROM conflict_instances x WHERE x.conflict_id = c.id)`

func scanConflict(sc interface{ Scan(...any) error }) (ConflictInfo, error) {
	var c ConflictInfo
	var kind string
	var coerced int
	var first, last, resolved int64
	err := sc.Scan(&c.ID, &kind, &c.State, &c.StudyUID, &c.SeriesUID, &c.ExistingPatientID, &c.ExistingStudyUID,
		&c.ExistingSeriesUID, &c.IncomingPatientID, &c.DisplayPatientID, &c.PatientName, &c.PatientBirthDate,
		&c.PatientSex, &c.StudyDate, &c.AccessionNumber, &c.StudyDescription, &c.SourceAE, &coerced, &first,
		&last, &resolved, &c.ResolvedBy, &c.ResolutionReason, &c.ResolutionAction, &c.Instances)
	if err != nil {
		return c, err
	}
	c.Kind = ConflictKind(kind)
	c.Coerced = coerced == 1
	c.FirstSeen = time.UnixMilli(first).UTC()
	c.LastSeen = time.UnixMilli(last).UTC()
	if resolved > 0 {
		t := time.UnixMilli(resolved).UTC()
		c.ResolvedAt = &t
	}
	return c, nil
}

// ListConflicts lists conflicts, newest activity first. filter is "active"
// (open and accepted, the default), "open", "accepted", "resolved" or "all".
func (s *SQL) ListConflicts(ctx context.Context, filter string, limit int) ([]ConflictInfo, error) {
	q := "SELECT " + conflictCols + " FROM conflicts c"
	var args []any
	switch filter {
	case "", "active":
		q += " WHERE c.active = 1"
	case StateOpen, StateAccepted:
		q += " WHERE c.active = 1 AND c.state = ?"
		args = append(args, filter)
	case "resolved":
		q += " WHERE c.active = 0"
	case "all":
	default:
		return nil, fmt.Errorf("index: unknown conflict filter %q", filter)
	}
	if limit <= 0 || limit > 1000 {
		limit = 1000
	}
	args = append(args, limit)
	rows, err := s.db.QueryContext(ctx, s.rebind(q+" ORDER BY c.last_seen DESC, c.id LIMIT ?"), args...)
	if err != nil {
		return nil, fmt.Errorf("index: list conflicts: %w", err)
	}
	defer rows.Close()
	var out []ConflictInfo
	for rows.Next() {
		c, err := scanConflict(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// GetConflict returns one conflict.
func (s *SQL) GetConflict(ctx context.Context, id string) (ConflictInfo, error) {
	c, err := scanConflict(s.db.QueryRowContext(ctx, s.rebind("SELECT "+conflictCols+" FROM conflicts c WHERE c.id = ?"), id))
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	return c, err
}

// ConflictInstances returns the held instances of a conflict with complete
// index records (patient and study attributes from the conflict row).
func (s *SQL) ConflictInstances(ctx context.Context, id string) ([]ConflictInstance, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(`
		SELECT ci.sop_instance_uid, ci.series_uid, ci.study_uid, ci.sop_class_uid, ci.instance_number,
			ci.transfer_syntax, ci.blob_key, ci.size_bytes, ci.source_ae, ci.received_at, ci.modality,
			ci.series_number, ci.series_description, ci.patient_id,
			c.patient_name, c.patient_birth_date, c.patient_sex, c.study_date, c.study_time,
			c.accession_number, c.study_id, c.study_description, c.referring_physician
		FROM conflict_instances ci JOIN conflicts c ON c.id = ci.conflict_id
		WHERE ci.conflict_id = ? ORDER BY ci.series_uid, ci.instance_number, ci.sop_instance_uid`), id)
	if err != nil {
		return nil, fmt.Errorf("index: conflict instances: %w", err)
	}
	defer rows.Close()
	var out []ConflictInstance
	for rows.Next() {
		var r Record
		var received int64
		if err := rows.Scan(&r.SOPInstanceUID, &r.SeriesUID, &r.StudyUID, &r.SOPClassUID, &r.InstanceNumber,
			&r.TransferSyntaxUID, &r.BlobKey, &r.SizeBytes, &r.SourceAE, &received, &r.Modality,
			&r.SeriesNumber, &r.SeriesDescription, &r.PatientID,
			&r.PatientName, &r.PatientBirthDate, &r.PatientSex, &r.StudyDate, &r.StudyTime,
			&r.AccessionNumber, &r.StudyID, &r.StudyDescription, &r.ReferringPhysician); err != nil {
			return nil, err
		}
		r.ReceivedAt = time.UnixMilli(received).UTC()
		out = append(out, ConflictInstance{ConflictID: id, Record: r, SOPInstanceUID: r.SOPInstanceUID,
			SeriesUID: r.SeriesUID, Modality: r.Modality, SeriesNumber: r.SeriesNumber,
			SeriesDescription: r.SeriesDescription, InstanceNumber: r.InstanceNumber,
			StoredPatientID: r.PatientID, ReceivedAt: r.ReceivedAt, SourceAE: r.SourceAE})
	}
	return out, rows.Err()
}

// RemoveConflictInstance drops a held instance once it has been moved into
// the index (correction is resumable instance by instance).
func (s *SQL) RemoveConflictInstance(ctx context.Context, id, sop string) error {
	_, err := s.db.ExecContext(ctx, s.rebind(`DELETE FROM conflict_instances WHERE conflict_id = ? AND sop_instance_uid = ?`), id, sop)
	return err
}

// ResolveConflict closes an active conflict. state is StateCorrected or
// StateRejected. Rejected conflicts keep their instance rows (hidden).
func (s *SQL) ResolveConflict(ctx context.Context, id, state, by, action, reason string) error {
	if state != StateCorrected && state != StateRejected {
		return fmt.Errorf("index: invalid resolution state %q", state)
	}
	res, err := s.db.ExecContext(ctx, s.rebind(`UPDATE conflicts SET active = 0, state = ?, resolved_at = ?,
		resolved_by = ?, resolution_action = ?, resolution_reason = ? WHERE id = ? AND active = 1`),
		state, time.Now().UnixMilli(), by, action, reason, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// CountOpenConflicts counts conflicts waiting for an administrator.
func (s *SQL) CountOpenConflicts(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, s.rebind(`SELECT COUNT(*) FROM conflicts WHERE active = 1 AND state = ?`), StateOpen).Scan(&n)
	return n, err
}

// StudyDemographics returns the indexed patient attributes of a study.
func (s *SQL) StudyDemographics(ctx context.Context, studyUID string) (Demographics, bool, error) {
	var d Demographics
	err := s.db.QueryRowContext(ctx, s.rebind(`SELECT patient_id, patient_name, patient_birth_date, patient_sex FROM studies WHERE study_uid = ?`), studyUID).
		Scan(&d.PatientID, &d.PatientName, &d.PatientBirthDate, &d.PatientSex)
	if errors.Is(err, sql.ErrNoRows) {
		return d, false, nil
	}
	return d, err == nil, err
}

// StudyInstances lists the indexed instances of a study.
func (s *SQL) StudyInstances(ctx context.Context, studyUID string) ([]InstanceRef, error) {
	rows, err := s.db.QueryContext(ctx, s.rebind(`SELECT sop_instance_uid, series_uid, transfer_syntax, blob_key
		FROM instances WHERE study_uid = ? ORDER BY series_uid, sop_instance_uid`), studyUID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []InstanceRef
	for rows.Next() {
		var r InstanceRef
		if err := rows.Scan(&r.SOPInstanceUID, &r.SeriesUID, &r.TransferSyntaxUID, &r.BlobKey); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SetStudyDemographics replaces a study's patient attributes (used only by
// the administrator's correct_existing action, DCM-SRS-078).
func (s *SQL) SetStudyDemographics(ctx context.Context, studyUID string, d Demographics) error {
	res, err := s.db.ExecContext(ctx, s.rebind(`UPDATE studies SET patient_id = ?, patient_name = ?, patient_name_fold = ?,
		patient_birth_date = ?, patient_sex = ?, updated_at = ? WHERE study_uid = ?`),
		d.PatientID, d.PatientName, Fold(d.PatientName), d.PatientBirthDate, d.PatientSex,
		time.Now().UTC().Format(time.RFC3339Nano), studyUID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// hasVisibleConflicts reports whether any conflict in the given states is
// active; when none is, queries use the plain tables (identical to before).
func (s *SQL) hasVisibleConflicts(ctx context.Context, states []string) (bool, error) {
	if len(states) == 0 {
		return false, nil
	}
	var one int
	err := s.db.QueryRowContext(ctx, `SELECT 1 FROM conflicts WHERE active = 1 AND state IN (`+quoteList(states)+`) LIMIT 1`).Scan(&one)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// quoteList renders fixed state constants (never user input) as SQL literals.
func quoteList(states []string) string {
	q := make([]string, len(states))
	for i, st := range states {
		q[i] = "'" + strings.ReplaceAll(st, "'", "") + "'"
	}
	return strings.Join(q, ",")
}
