// Package index keeps the queryable patient/study/series/instance metadata.
// One SQL implementation serves two engines:
//
//   - sqlite   (pure Go, no CGO) for single-node and edge installs
//   - postgres for clustered deployments where many gateway nodes share state
//
// The gateway translates DICOM query keys into Filters; this package only
// knows about SQL.
package index

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib" // driver "pgx"
	_ "modernc.org/sqlite"             // driver "sqlite"
)

// Level is a query/retrieve level.
type Level string

const (
	Patient Level = "PATIENT"
	Study   Level = "STUDY"
	Series  Level = "SERIES"
	Image   Level = "IMAGE"
)

// Op is a filter operation.
type Op int

const (
	OpEq               Op = iota // single value
	OpIn                         // UID list
	OpLike                       // DICOM wildcard (* ?), already translated to SQL LIKE pattern
	OpRange                      // inclusive range; empty bound = open
	OpStudyHasModality           // study has at least one series with a modality in Values
	OpEqFold                     // case-insensitive equality; Column is a *_fold column, Values pre-folded
	OpLikeFold                   // case-insensitive LIKE on a *_fold column
)

// Fold is the case folding used for case-insensitive person name matching.
// It is done in Go, not SQL, because SQLite's LOWER() only folds ASCII.
func Fold(s string) string { return strings.ToLower(s) }

// Filter restricts a query on one column.
type Filter struct {
	Column string // qualified column, e.g. "st.study_date"
	Op     Op
	Values []string // OpEq: [v]; OpIn: list; OpLike: [pattern]; OpRange: [from, to]
}

// Record is what is written when an instance is stored.
type Record struct {
	PatientID, PatientName, PatientBirthDate, PatientSex           string
	StudyUID, StudyDate, StudyTime, AccessionNumber, StudyID       string
	StudyDescription, ReferringPhysician                           string
	SeriesUID, Modality, SeriesNumber, SeriesDescription           string
	SOPInstanceUID, SOPClassUID, InstanceNumber, TransferSyntaxUID string
	BlobKey, SourceAE                                              string
	SizeBytes                                                      int64
	ReceivedAt                                                     time.Time
}

// OutboxEvent is an event written in the same transaction as the instance it
// describes, so it can't be lost if the event bus is down. A relay publishes
// it later and deletes it once the bus has acknowledged it.
type OutboxEvent struct {
	MsgID    string // stable id, used by the bus to de-duplicate retries
	Subject  string
	Payload  []byte // JSON
	Attempts int
}

// Row is one query result, keyed by DICOM keyword (plus internal "_" keys).
type Row map[string]string

// Index is the metadata store contract.
type Index interface {
	Upsert(ctx context.Context, r Record) error
	// UpsertWithEvent is Upsert plus an outbox event in the same transaction.
	UpsertWithEvent(ctx context.Context, r Record, ev *OutboxEvent) error
	// ClaimEvents leases up to limit due events for publishing. A leased event
	// is not handed out again until the lease expires, so several nodes can
	// drain one shared outbox.
	ClaimEvents(ctx context.Context, limit int, lease time.Duration) ([]OutboxEvent, error)
	AckEvent(ctx context.Context, msgID string) error
	NackEvent(ctx context.Context, msgID string, retryAt time.Time, errText string) error
	PendingEvents(ctx context.Context) (int, error)
	Find(ctx context.Context, level Level, filters []Filter, limit int) ([]Row, error)
	ModalitiesForStudies(ctx context.Context, studyUIDs []string) (map[string][]string, error)
	Ping(ctx context.Context) error
	Close() error
	Conflicts
}

// SQL implements Index over database/sql.
type SQL struct {
	db       *sql.DB
	postgres bool
}

// Open connects and applies the schema. driver is "sqlite" or "postgres".
func Open(ctx context.Context, driver, dsn string) (*SQL, error) {
	s := &SQL{}
	switch driver {
	case "sqlite":
		if !strings.Contains(dsn, "_pragma") {
			sep := "?"
			if strings.Contains(dsn, "?") {
				sep = "&"
			}
			dsn += sep + "_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)"
		}
		// SQLite's LIKE ignores ASCII case; PostgreSQL's doesn't. Make SQLite
		// match so wildcard queries behave the same on both engines
		// (case-insensitive name matching uses the folded column instead).
		dsn += "&_pragma=case_sensitive_like(1)"
		db, err := sql.Open("sqlite", dsn)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(1) // SQLite has one writer; serialize to avoid SQLITE_BUSY.
		s.db = db
	case "postgres":
		db, err := sql.Open("pgx", dsn)
		if err != nil {
			return nil, err
		}
		db.SetMaxOpenConns(20)
		db.SetMaxIdleConns(5)
		db.SetConnMaxLifetime(30 * time.Minute)
		s.db, s.postgres = db, true
	default:
		return nil, fmt.Errorf("index: unknown driver %q (use sqlite or postgres)", driver)
	}
	if err := s.migrate(ctx); err != nil {
		_ = s.db.Close()
		return nil, err
	}
	return s, nil
}

// schemaVersion is bumped when migrations are appended below.
const schemaVersion = 4

var migrations = []string{
	`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL)`,
	`CREATE TABLE IF NOT EXISTS studies (
		study_uid           TEXT PRIMARY KEY,
		patient_id          TEXT NOT NULL DEFAULT '',
		patient_name        TEXT NOT NULL DEFAULT '',
		patient_birth_date  TEXT NOT NULL DEFAULT '',
		patient_sex         TEXT NOT NULL DEFAULT '',
		study_date          TEXT NOT NULL DEFAULT '',
		study_time          TEXT NOT NULL DEFAULT '',
		accession_number    TEXT NOT NULL DEFAULT '',
		study_id            TEXT NOT NULL DEFAULT '',
		study_description   TEXT NOT NULL DEFAULT '',
		referring_physician TEXT NOT NULL DEFAULT '',
		updated_at          TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS series (
		series_uid          TEXT PRIMARY KEY,
		study_uid           TEXT NOT NULL,
		modality            TEXT NOT NULL DEFAULT '',
		series_number       TEXT NOT NULL DEFAULT '',
		series_description  TEXT NOT NULL DEFAULT '',
		updated_at          TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS instances (
		sop_instance_uid    TEXT PRIMARY KEY,
		series_uid          TEXT NOT NULL,
		study_uid           TEXT NOT NULL,
		sop_class_uid       TEXT NOT NULL,
		instance_number     TEXT NOT NULL DEFAULT '',
		transfer_syntax     TEXT NOT NULL,
		blob_key            TEXT NOT NULL,
		size_bytes          BIGINT NOT NULL DEFAULT 0,
		source_ae           TEXT NOT NULL DEFAULT '',
		received_at         TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_studies_patient ON studies(patient_id)`,
	`CREATE INDEX IF NOT EXISTS idx_studies_date ON studies(study_date)`,
	`CREATE INDEX IF NOT EXISTS idx_studies_accession ON studies(accession_number)`,
	`CREATE INDEX IF NOT EXISTS idx_series_study ON series(study_uid)`,
	`CREATE INDEX IF NOT EXISTS idx_instances_series ON instances(series_uid)`,
	`CREATE INDEX IF NOT EXISTS idx_instances_study ON instances(study_uid)`,
	// v2: event outbox. Times are Unix milliseconds.
	`CREATE TABLE IF NOT EXISTS event_outbox (
		msg_id          TEXT PRIMARY KEY,
		subject         TEXT NOT NULL,
		payload         TEXT NOT NULL,
		created_at      BIGINT NOT NULL,
		next_attempt_at BIGINT NOT NULL,
		attempts        INTEGER NOT NULL DEFAULT 0,
		last_error      TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE INDEX IF NOT EXISTS idx_outbox_due ON event_outbox(next_attempt_at)`,
	// v4: patient association conflicts (DCM-SRS-070…079). Times are Unix ms.
	`CREATE TABLE IF NOT EXISTS conflicts (
		id                  TEXT PRIMARY KEY,
		group_key           TEXT NOT NULL,
		kind                TEXT NOT NULL,
		active              INTEGER NOT NULL DEFAULT 1,
		state               TEXT NOT NULL,
		study_uid           TEXT NOT NULL,
		series_uid          TEXT NOT NULL DEFAULT '',
		sop_instance_uid    TEXT NOT NULL DEFAULT '',
		existing_patient_id TEXT NOT NULL DEFAULT '',
		existing_study_uid  TEXT NOT NULL DEFAULT '',
		existing_series_uid TEXT NOT NULL DEFAULT '',
		incoming_patient_id TEXT NOT NULL DEFAULT '',
		display_patient_id  TEXT NOT NULL DEFAULT '',
		patient_name        TEXT NOT NULL DEFAULT '',
		patient_name_fold   TEXT NOT NULL DEFAULT '',
		patient_birth_date  TEXT NOT NULL DEFAULT '',
		patient_sex         TEXT NOT NULL DEFAULT '',
		study_date          TEXT NOT NULL DEFAULT '',
		study_time          TEXT NOT NULL DEFAULT '',
		accession_number    TEXT NOT NULL DEFAULT '',
		study_id            TEXT NOT NULL DEFAULT '',
		study_description   TEXT NOT NULL DEFAULT '',
		referring_physician TEXT NOT NULL DEFAULT '',
		source_ae           TEXT NOT NULL DEFAULT '',
		coerced             INTEGER NOT NULL DEFAULT 0,
		first_seen          BIGINT NOT NULL,
		last_seen           BIGINT NOT NULL,
		resolved_at         BIGINT NOT NULL DEFAULT 0,
		resolved_by         TEXT NOT NULL DEFAULT '',
		resolution_action   TEXT NOT NULL DEFAULT '',
		resolution_reason   TEXT NOT NULL DEFAULT ''
	)`,
	`CREATE UNIQUE INDEX IF NOT EXISTS idx_conflicts_group ON conflicts(group_key) WHERE active = 1`,
	`CREATE INDEX IF NOT EXISTS idx_conflicts_active ON conflicts(active, state)`,
	`CREATE TABLE IF NOT EXISTS conflict_instances (
		conflict_id         TEXT NOT NULL,
		sop_instance_uid    TEXT NOT NULL,
		series_uid          TEXT NOT NULL,
		study_uid           TEXT NOT NULL,
		sop_class_uid       TEXT NOT NULL,
		instance_number     TEXT NOT NULL DEFAULT '',
		transfer_syntax     TEXT NOT NULL,
		blob_key            TEXT NOT NULL,
		size_bytes          BIGINT NOT NULL DEFAULT 0,
		source_ae           TEXT NOT NULL DEFAULT '',
		received_at         BIGINT NOT NULL,
		modality            TEXT NOT NULL DEFAULT '',
		series_number       TEXT NOT NULL DEFAULT '',
		series_description  TEXT NOT NULL DEFAULT '',
		patient_id          TEXT NOT NULL DEFAULT '',
		PRIMARY KEY (conflict_id, sop_instance_uid)
	)`,
}

// versionedMigrations run once, when upgrading from an older schema version.
// They hold changes (like ADD COLUMN) that can't be written idempotently for
// both engines.
var versionedMigrations = map[int][]string{
	3: {`ALTER TABLE studies ADD COLUMN patient_name_fold TEXT NOT NULL DEFAULT ''`},
}

// migrationLockKey serializes schema changes across cluster nodes starting at
// the same time (PostgreSQL DDL is not safe to run concurrently).
const migrationLockKey = 7_311_400_101

func (s *SQL) migrate(ctx context.Context) error {
	conn, err := s.db.Conn(ctx) // one session, so the advisory lock is held throughout
	if err != nil {
		return err
	}
	defer conn.Close()
	if s.postgres {
		if _, err := conn.ExecContext(ctx, `SELECT pg_advisory_lock($1)`, migrationLockKey); err != nil {
			return fmt.Errorf("index: migration lock: %w", err)
		}
		defer func() {
			_, _ = conn.ExecContext(context.Background(), `SELECT pg_advisory_unlock($1)`, migrationLockKey)
		}()
	}
	for _, m := range migrations {
		if _, err := conn.ExecContext(ctx, m); err != nil {
			return fmt.Errorf("index: migrate: %w", err)
		}
	}
	var n int
	if err := conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM schema_version`).Scan(&n); err != nil {
		return err
	}
	current := 0 // fresh database: base tables were just created at version 2's shape
	if n == 0 {
		if _, err := conn.ExecContext(ctx, s.rebind(`INSERT INTO schema_version(version) VALUES (?)`), 2); err != nil {
			return err
		}
		current = 2
	} else if err := conn.QueryRowContext(ctx, `SELECT MAX(version) FROM schema_version`).Scan(&current); err != nil {
		return err
	}
	if current < 2 {
		current = 2 // v2 (outbox) is created idempotently above
	}
	for v := current + 1; v <= schemaVersion; v++ {
		for _, m := range versionedMigrations[v] {
			if _, err := conn.ExecContext(ctx, m); err != nil {
				return fmt.Errorf("index: migrate to v%d: %w", v, err)
			}
		}
		if v == 3 {
			if err := backfillFold(ctx, conn, s); err != nil {
				return fmt.Errorf("index: migrate to v3: %w", err)
			}
		}
		if _, err := conn.ExecContext(ctx, s.rebind(`UPDATE schema_version SET version = ?`), v); err != nil {
			return err
		}
	}
	return nil
}

// backfillFold fills patient_name_fold for studies stored before schema v3.
func backfillFold(ctx context.Context, conn *sql.Conn, s *SQL) error {
	rows, err := conn.QueryContext(ctx, `SELECT study_uid, patient_name FROM studies`)
	if err != nil {
		return err
	}
	type pair struct{ uid, name string }
	var all []pair
	for rows.Next() {
		var p pair
		if err := rows.Scan(&p.uid, &p.name); err != nil {
			_ = rows.Close()
			return err
		}
		all = append(all, p)
	}
	_ = rows.Close()
	if err := rows.Err(); err != nil {
		return err
	}
	for _, p := range all {
		if _, err := conn.ExecContext(ctx, s.rebind(`UPDATE studies SET patient_name_fold = ? WHERE study_uid = ?`), Fold(p.name), p.uid); err != nil {
			return err
		}
	}
	return nil
}

// rebind converts ? placeholders to $n for PostgreSQL.
func (s *SQL) rebind(q string) string {
	if !s.postgres {
		return q
	}
	var b strings.Builder
	n := 0
	for _, r := range q {
		if r == '?' {
			n++
			b.WriteString("$" + strconv.Itoa(n))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Upsert writes study, series and instance rows in one transaction. A resent
// instance (same SOP Instance UID, same study and series) replaces the
// previous metadata. A record that conflicts with the indexed patient, study
// or series association is not written: Upsert returns *ConflictError and the
// existing associations stay as they are (DCM-SRS-070). For the same Patient
// ID, the first-registered name, birth date and sex are kept (DCM-SRS-076).
func (s *SQL) Upsert(ctx context.Context, r Record) error { return s.UpsertWithEvent(ctx, r, nil) }

// UpsertWithEvent writes the instance and, when ev is not nil, its outbox
// event atomically: either both are stored or neither is.
func (s *SQL) UpsertWithEvent(ctx context.Context, r Record, ev *OutboxEvent) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if r.ReceivedAt.IsZero() {
		r.ReceivedAt = time.Now()
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// Insert the study if it is new; an existing row keeps its patient. The
	// check below then reads the row that won (locked on PostgreSQL), so two
	// nodes storing different patients for one new study can't both succeed.
	if _, err := tx.ExecContext(ctx, s.rebind(`
		INSERT INTO studies (study_uid, patient_id, patient_name, patient_birth_date, patient_sex,
			study_date, study_time, accession_number, study_id, study_description, referring_physician, updated_at,
			patient_name_fold)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (study_uid) DO NOTHING`),
		r.StudyUID, r.PatientID, r.PatientName, r.PatientBirthDate, r.PatientSex,
		r.StudyDate, r.StudyTime, r.AccessionNumber, r.StudyID, r.StudyDescription, r.ReferringPhysician, now,
		Fold(r.PatientName),
	); err != nil {
		return fmt.Errorf("index: upsert study: %w", err)
	}
	chk, err := s.check(ctx, tx, r, true)
	if err != nil {
		return fmt.Errorf("index: conflict check: %w", err)
	}
	if chk.Conflict != nil {
		return &ConflictError{Conflict: *chk.Conflict}
	}
	// Study-level attributes follow the newest instance; patient attributes don't.
	if _, err := tx.ExecContext(ctx, s.rebind(`
		UPDATE studies SET study_date = ?, study_time = ?, accession_number = ?, study_id = ?,
			study_description = ?, referring_physician = ?, updated_at = ?
		WHERE study_uid = ?`),
		r.StudyDate, r.StudyTime, r.AccessionNumber, r.StudyID, r.StudyDescription, r.ReferringPhysician, now,
		r.StudyUID,
	); err != nil {
		return fmt.Errorf("index: update study: %w", err)
	}
	if _, err := tx.ExecContext(ctx, s.rebind(`
		INSERT INTO series (series_uid, study_uid, modality, series_number, series_description, updated_at)
		VALUES (?,?,?,?,?,?)
		ON CONFLICT (series_uid) DO UPDATE SET
			modality=excluded.modality,
			series_number=excluded.series_number, series_description=excluded.series_description,
			updated_at=excluded.updated_at`),
		r.SeriesUID, r.StudyUID, r.Modality, r.SeriesNumber, r.SeriesDescription, now,
	); err != nil {
		return fmt.Errorf("index: upsert series: %w", err)
	}
	if _, err := tx.ExecContext(ctx, s.rebind(`
		INSERT INTO instances (sop_instance_uid, series_uid, study_uid, sop_class_uid, instance_number,
			transfer_syntax, blob_key, size_bytes, source_ae, received_at)
		VALUES (?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT (sop_instance_uid) DO UPDATE SET
			sop_class_uid=excluded.sop_class_uid,
			instance_number=excluded.instance_number, transfer_syntax=excluded.transfer_syntax,
			blob_key=excluded.blob_key, size_bytes=excluded.size_bytes, source_ae=excluded.source_ae,
			received_at=excluded.received_at`),
		r.SOPInstanceUID, r.SeriesUID, r.StudyUID, r.SOPClassUID, r.InstanceNumber,
		r.TransferSyntaxUID, r.BlobKey, r.SizeBytes, r.SourceAE, r.ReceivedAt.UTC().Format(time.RFC3339Nano),
	); err != nil {
		return fmt.Errorf("index: upsert instance: %w", err)
	}
	if ev != nil {
		ms := time.Now().UnixMilli()
		if _, err := tx.ExecContext(ctx, s.rebind(`
			INSERT INTO event_outbox (msg_id, subject, payload, created_at, next_attempt_at)
			VALUES (?,?,?,?,?) ON CONFLICT (msg_id) DO NOTHING`),
			ev.MsgID, ev.Subject, string(ev.Payload), ms, ms,
		); err != nil {
			return fmt.Errorf("index: outbox event: %w", err)
		}
	}
	return tx.Commit()
}

// column maps result columns to DICOM keywords per level.
type column struct{ expr, keyword string }

// Column expressions may hold placeholders that differ when recorded
// conflicts are included (see sources): {SERIES}, {INSTANCES} name the series
// and instance sources, {CID_ST}/{CID_SE} add the conflict-id join condition.
var levelColumns = map[Level][]column{
	Patient: {
		{"st.patient_id", "PatientID"}, {"MAX(st.patient_name)", "PatientName"},
		{"MAX(st.patient_birth_date)", "PatientBirthDate"}, {"MAX(st.patient_sex)", "PatientSex"},
		{"COUNT(*)", "NumberOfPatientRelatedStudies"},
	},
	Study: {
		{"st.patient_id", "PatientID"}, {"st.patient_name", "PatientName"},
		{"st.patient_birth_date", "PatientBirthDate"}, {"st.patient_sex", "PatientSex"},
		{"st.study_uid", "StudyInstanceUID"}, {"st.study_date", "StudyDate"}, {"st.study_time", "StudyTime"},
		{"st.accession_number", "AccessionNumber"}, {"st.study_id", "StudyID"},
		{"st.study_description", "StudyDescription"}, {"st.referring_physician", "ReferringPhysicianName"},
		{"(SELECT COUNT(*) FROM {SERIES} x WHERE x.study_uid = st.study_uid{CID_ST})", "NumberOfStudyRelatedSeries"},
		{"(SELECT COUNT(*) FROM {INSTANCES} x WHERE x.study_uid = st.study_uid{CID_ST})", "NumberOfStudyRelatedInstances"},
	},
	Series: {
		{"st.patient_id", "PatientID"}, {"st.study_uid", "StudyInstanceUID"},
		{"se.series_uid", "SeriesInstanceUID"}, {"se.modality", "Modality"},
		{"se.series_number", "SeriesNumber"}, {"se.series_description", "SeriesDescription"},
		{"(SELECT COUNT(*) FROM {INSTANCES} x WHERE x.series_uid = se.series_uid{CID_SE})", "NumberOfSeriesRelatedInstances"},
	},
	Image: {
		{"st.patient_id", "PatientID"}, {"st.study_uid", "StudyInstanceUID"},
		{"se.series_uid", "SeriesInstanceUID"}, {"se.modality", "Modality"},
		{"i.sop_instance_uid", "SOPInstanceUID"}, {"i.sop_class_uid", "SOPClassUID"},
		{"i.instance_number", "InstanceNumber"},
		{"i.transfer_syntax", "_TransferSyntaxUID"}, {"i.blob_key", "_BlobKey"},
	},
}

var levelFrom = map[Level]string{
	Patient: `{STUDIES} st`,
	Study:   `{STUDIES} st`,
	Series:  `{SERIES} se JOIN {STUDIES} st ON st.study_uid = se.study_uid{CID_SE_ST}`,
	Image: `{INSTANCES} i JOIN {SERIES} se ON se.series_uid = i.series_uid{CID_I_SE}
	        JOIN {STUDIES} st ON st.study_uid = i.study_uid{CID_I_ST}`,
}

// sources returns the placeholder replacer. Without conflicts it yields the
// plain tables, so the SQL is exactly what it was before conflicts existed.
// With conflicts, each source is the table UNION ALL the visible conflict
// rows, tagged with a cid column (” for indexed rows) that keeps a held
// instance with its conflict's patient even when it shares a Study UID.
func sources(states []string) *strings.Replacer {
	if len(states) == 0 {
		return strings.NewReplacer("{STUDIES}", "studies", "{SERIES}", "series", "{INSTANCES}", "instances",
			"{CID_ST}", "", "{CID_SE}", "", "{CID_SE_ST}", "", "{CID_I_SE}", "", "{CID_I_ST}", "")
	}
	cond := "c.active = 1 AND c.state IN (" + quoteList(states) + ")"
	studies := `(SELECT study_uid, patient_id, patient_name, patient_name_fold, patient_birth_date, patient_sex,
			study_date, study_time, accession_number, study_id, study_description, referring_physician, '' AS cid
		FROM studies
		UNION ALL SELECT c.study_uid, c.display_patient_id, c.patient_name, c.patient_name_fold, c.patient_birth_date,
			c.patient_sex, c.study_date, c.study_time, c.accession_number, c.study_id, c.study_description,
			c.referring_physician, c.id FROM conflicts c WHERE ` + cond + `)`
	series := `(SELECT series_uid, study_uid, modality, series_number, series_description, '' AS cid FROM series
		UNION ALL SELECT ci.series_uid, ci.study_uid, MAX(ci.modality), MAX(ci.series_number),
			MAX(ci.series_description), ci.conflict_id
		FROM conflict_instances ci JOIN conflicts c ON c.id = ci.conflict_id WHERE ` + cond + `
		GROUP BY ci.conflict_id, ci.series_uid, ci.study_uid)`
	instances := `(SELECT sop_instance_uid, series_uid, study_uid, sop_class_uid, instance_number, transfer_syntax,
			blob_key, '' AS cid FROM instances
		UNION ALL SELECT ci.sop_instance_uid, ci.series_uid, ci.study_uid, ci.sop_class_uid, ci.instance_number,
			ci.transfer_syntax, ci.blob_key, ci.conflict_id
		FROM conflict_instances ci JOIN conflicts c ON c.id = ci.conflict_id WHERE ` + cond + `)`
	return strings.NewReplacer("{STUDIES}", studies, "{SERIES}", series, "{INSTANCES}", instances,
		"{CID_ST}", " AND x.cid = st.cid", "{CID_SE}", " AND x.cid = se.cid",
		"{CID_SE_ST}", " AND st.cid = se.cid", "{CID_I_SE}", " AND se.cid = i.cid", "{CID_I_ST}", " AND st.cid = i.cid")
}

// Find runs a query at level with the given filters (AND-ed). Recorded
// conflicts are not included; the gateway uses FindWith.
func (s *SQL) Find(ctx context.Context, level Level, filters []Filter, limit int) ([]Row, error) {
	return s.FindWith(ctx, level, filters, limit, FindOptions{})
}

// FindWith is Find, optionally including held or accepted conflict instances
// as separate patient/study entries (DCM-SRS-073). Rows from a conflict carry
// its id in "_ConflictID".
func (s *SQL) FindWith(ctx context.Context, level Level, filters []Filter, limit int, opts FindOptions) ([]Row, error) {
	cols, ok := levelColumns[level]
	if !ok {
		return nil, fmt.Errorf("index: unsupported level %q", level)
	}
	states := opts.states()
	if len(states) > 0 {
		visible, err := s.hasVisibleConflicts(ctx, states)
		if err != nil {
			return nil, fmt.Errorf("index: find: %w", err)
		}
		if !visible {
			states = nil
		}
	}
	rep := sources(states)
	if len(states) > 0 && level != Patient {
		cols = append(append([]column(nil), cols...), column{"st.cid", "_ConflictID"})
	}
	exprs := make([]string, len(cols))
	for i, c := range cols {
		exprs[i] = rep.Replace(c.expr)
	}
	where, args := buildWhere(filters, rep)
	q := "SELECT " + strings.Join(exprs, ", ") + " FROM " + rep.Replace(levelFrom[level]) + where
	switch level {
	case Patient:
		q += " GROUP BY st.patient_id ORDER BY st.patient_id"
	case Study:
		q += " ORDER BY st.study_date DESC, st.study_uid"
	case Series:
		q += " ORDER BY se.series_number, se.series_uid"
	case Image:
		q += " ORDER BY se.series_uid, i.instance_number, i.sop_instance_uid"
	}
	if len(states) > 0 && level != Patient {
		q += ", st.cid"
	}
	if limit > 0 {
		q += " LIMIT " + strconv.Itoa(limit)
	}

	rows, err := s.db.QueryContext(ctx, s.rebind(q), args...)
	if err != nil {
		return nil, fmt.Errorf("index: find: %w", err)
	}
	defer rows.Close()

	var out []Row
	for rows.Next() {
		vals := make([]sql.NullString, len(cols))
		ptrs := make([]any, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(Row, len(cols))
		for i, c := range cols {
			row[c.keyword] = vals[i].String
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func buildWhere(filters []Filter, rep *strings.Replacer) (string, []any) {
	if len(filters) == 0 {
		return "", nil
	}
	var parts []string
	var args []any
	for _, f := range filters {
		switch f.Op {
		case OpEq:
			parts = append(parts, f.Column+" = ?")
			args = append(args, f.Values[0])
		case OpIn:
			ph := strings.TrimSuffix(strings.Repeat("?,", len(f.Values)), ",")
			parts = append(parts, f.Column+" IN ("+ph+")")
			for _, v := range f.Values {
				args = append(args, v)
			}
		case OpLike:
			parts = append(parts, f.Column+` LIKE ? ESCAPE '\'`)
			args = append(args, f.Values[0])
		case OpEqFold:
			parts = append(parts, f.Column+" = ?")
			args = append(args, Fold(f.Values[0]))
		case OpLikeFold:
			// SQLite's LIKE is ASCII case-insensitive and PostgreSQL's is
			// case-sensitive; both match correctly on pre-folded values.
			parts = append(parts, f.Column+` LIKE ? ESCAPE '\'`)
			args = append(args, Fold(f.Values[0]))
		case OpStudyHasModality:
			ph := strings.TrimSuffix(strings.Repeat("?,", len(f.Values)), ",")
			parts = append(parts, rep.Replace("EXISTS (SELECT 1 FROM {SERIES} x WHERE x.study_uid = st.study_uid{CID_ST} AND x.modality IN (")+ph+"))")
			for _, v := range f.Values {
				args = append(args, v)
			}
		case OpRange:
			if f.Values[0] != "" {
				parts = append(parts, f.Column+" >= ?")
				args = append(args, f.Values[0])
			}
			if f.Values[1] != "" {
				parts = append(parts, f.Column+" <= ?")
				args = append(args, f.Values[1])
			}
		}
	}
	if len(parts) == 0 {
		return "", nil
	}
	return " WHERE " + strings.Join(parts, " AND "), args
}

// ModalitiesForStudies returns the distinct series modalities per study.
func (s *SQL) ModalitiesForStudies(ctx context.Context, studyUIDs []string) (map[string][]string, error) {
	out := map[string][]string{}
	if len(studyUIDs) == 0 {
		return out, nil
	}
	ph := strings.TrimSuffix(strings.Repeat("?,", len(studyUIDs)), ",")
	args := make([]any, len(studyUIDs))
	for i, u := range studyUIDs {
		args[i] = u
	}
	rows, err := s.db.QueryContext(ctx, s.rebind(
		`SELECT DISTINCT study_uid, modality FROM series WHERE modality <> '' AND study_uid IN (`+ph+`) ORDER BY study_uid, modality`), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var st, mod string
		if err := rows.Scan(&st, &mod); err != nil {
			return nil, err
		}
		out[st] = append(out[st], mod)
	}
	return out, rows.Err()
}

// ClaimEvents leases due events, oldest first. The outer next_attempt_at
// condition is re-checked by PostgreSQL after a row lock wait, so two nodes
// claiming at once never get the same event.
func (s *SQL) ClaimEvents(ctx context.Context, limit int, lease time.Duration) ([]OutboxEvent, error) {
	now := time.Now().UnixMilli()
	rows, err := s.db.QueryContext(ctx, s.rebind(`
		UPDATE event_outbox SET next_attempt_at = ?, attempts = attempts + 1
		WHERE next_attempt_at <= ? AND msg_id IN (
			SELECT msg_id FROM event_outbox WHERE next_attempt_at <= ?
			ORDER BY created_at, msg_id LIMIT ?)
		RETURNING msg_id, subject, payload, attempts`),
		now+lease.Milliseconds(), now, now, limit)
	if err != nil {
		return nil, fmt.Errorf("index: claim events: %w", err)
	}
	defer rows.Close()
	var out []OutboxEvent
	for rows.Next() {
		var e OutboxEvent
		var payload string
		if err := rows.Scan(&e.MsgID, &e.Subject, &payload, &e.Attempts); err != nil {
			return nil, err
		}
		e.Payload = []byte(payload)
		out = append(out, e)
	}
	return out, rows.Err()
}

// AckEvent removes a published event.
func (s *SQL) AckEvent(ctx context.Context, msgID string) error {
	_, err := s.db.ExecContext(ctx, s.rebind(`DELETE FROM event_outbox WHERE msg_id = ?`), msgID)
	return err
}

// NackEvent schedules a failed event for another attempt.
func (s *SQL) NackEvent(ctx context.Context, msgID string, retryAt time.Time, errText string) error {
	_, err := s.db.ExecContext(ctx, s.rebind(`UPDATE event_outbox SET next_attempt_at = ?, last_error = ? WHERE msg_id = ?`),
		retryAt.UnixMilli(), errText, msgID)
	return err
}

// PendingEvents counts events not yet published.
func (s *SQL) PendingEvents(ctx context.Context) (int, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM event_outbox`).Scan(&n)
	return n, err
}

// Backup writes a consistent copy of a SQLite index to dest while the module
// keeps running (VACUUM INTO takes a read transaction, so concurrent writers
// are not blocked and the copy is a single point-in-time snapshot). An
// existing file at dest is replaced. PostgreSQL indexes are backed up with the
// database's own tools (pg_dump), so this returns an error for them.
func (s *SQL) Backup(ctx context.Context, dest string) error {
	if s.postgres {
		return fmt.Errorf("index: backup of a PostgreSQL index uses pg_dump, not -backup")
	}
	if dest == "" {
		return fmt.Errorf("index: backup destination is empty")
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return fmt.Errorf("index: backup: %w", err)
	}
	tmp := dest + ".partial"
	_ = os.Remove(tmp)
	if _, err := s.db.ExecContext(ctx, `VACUUM INTO ?`, tmp); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("index: backup: %w", err)
	}
	if err := os.Rename(tmp, dest); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("index: backup: %w", err)
	}
	return nil
}

func (s *SQL) Ping(ctx context.Context) error { return s.db.PingContext(ctx) }
func (s *SQL) Close() error                   { return s.db.Close() }
