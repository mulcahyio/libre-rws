package accounts

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func init() { ArgonMemory, ArgonTime, ArgonThreads = 1024, 1, 1 } // fast hashes in tests

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(t.TempDir(), "wt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// First-run setup creates exactly one administrator and can't be replayed;
// there are no default credentials.
// Verifies: WT-SRS-002
func TestFirstAdminOnce(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if has, _ := s.HasUsers(ctx); has {
		t.Fatal("fresh store has users")
	}
	for _, u := range []string{"admin", "root", "librewt"} {
		if _, err := s.Authenticate(ctx, u, "admin"); !errors.Is(err, ErrBadCredentials) {
			t.Fatalf("default credential %s works: %v", u, err)
		}
	}
	u, err := s.CreateFirstAdmin(ctx, "Pat.Admin", "correct horse battery")
	if err != nil || u.Role != Admin || u.Username != "pat.admin" {
		t.Fatalf("u=%+v err=%v", u, err)
	}
	if _, err := s.CreateFirstAdmin(ctx, "eve", "correct horse battery"); !errors.Is(err, ErrSetupDone) {
		t.Fatalf("setup replayed: %v", err)
	}
}

// Passwords are stored as argon2id hashes, never in clear; the policy is
// enforced; wrong passwords and disabled accounts fail alike.
// Verifies: WT-SRS-003
func TestPasswordsArgon2id(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	u, err := s.CreateUser(ctx, "viewer1", "a long enough password", Viewer)
	if err != nil {
		t.Fatal(err)
	}
	var hash string
	if err := s.db.QueryRowContext(ctx, `SELECT pw_hash FROM users WHERE id = ?`, u.ID).Scan(&hash); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$") || strings.Contains(hash, "a long enough password") {
		t.Fatalf("hash = %s", hash)
	}
	if _, err := s.Authenticate(ctx, "VIEWER1", "a long enough password"); err != nil {
		t.Fatalf("login: %v", err)
	}
	if _, err := s.Authenticate(ctx, "viewer1", "wrong password!!"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("wrong password: %v", err)
	}
	for _, pw := range []string{"short", "viewer2viewer2"[:7]} {
		if _, err := s.CreateUser(ctx, "viewer2", pw, Viewer); err == nil {
			t.Fatalf("weak password %q accepted", pw)
		}
	}
	if _, err := s.CreateUser(ctx, "samepassword", "samepassword", Viewer); err == nil {
		t.Fatal("password equal to username accepted")
	}
	if _, err := s.CreateUser(ctx, "bad name", "a long enough password", Viewer); err == nil {
		t.Fatal("invalid username accepted")
	}
	if _, err := s.CreateUser(ctx, "viewer1", "a long enough password", Viewer); !errors.Is(err, ErrExists) {
		t.Fatalf("duplicate: %v", err)
	}
	if err := s.SetDisabled(ctx, u.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(ctx, "viewer1", "a long enough password"); !errors.Is(err, ErrBadCredentials) {
		t.Fatalf("disabled login: %v", err)
	}
}

// Role, password and disabled changes bump the epoch (ending sessions), and
// the last active administrator can't be disabled or demoted.
// Verifies: WT-SRS-004, WT-SRS-005
func TestRolesEpochAndLastAdmin(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	a, _ := s.CreateFirstAdmin(ctx, "admin1", "admin one password")
	if err := s.SetRole(ctx, a.ID, Viewer); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("demote last admin: %v", err)
	}
	if err := s.SetDisabled(ctx, a.ID, true); !errors.Is(err, ErrLastAdmin) {
		t.Fatalf("disable last admin: %v", err)
	}
	b, _ := s.CreateUser(ctx, "admin2", "admin two password", Admin)
	if err := s.SetRole(ctx, a.ID, Operator); err != nil {
		t.Fatal(err)
	}
	got, _ := s.Get(ctx, a.ID)
	if got.Role != Operator || got.Epoch != a.Epoch+1 {
		t.Fatalf("after role change: %+v", got)
	}
	if err := s.SetPassword(ctx, b.ID, "admin two new password"); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.Get(ctx, b.ID); got.Epoch != b.Epoch+1 {
		t.Fatalf("password change didn't bump epoch: %+v", got)
	}
	if err := s.SetRole(ctx, b.ID, "superuser"); err == nil {
		t.Fatal("unknown role accepted")
	}
}

// Verifies: WT-SRS-011
func TestAuditLog(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for i, a := range []string{"login", "exception.resolve"} {
		if err := s.Audit(ctx, Entry{User: "admin1", IP: "127.0.0.1", Action: a, Target: "c1", Outcome: "success", Detail: string(rune('a' + i))}); err != nil {
			t.Fatal(err)
		}
	}
	log, err := s.AuditLog(ctx, 0)
	if err != nil || len(log) != 2 || log[0].Action != "exception.resolve" || log[1].IP != "127.0.0.1" {
		t.Fatalf("log = %+v, %v", log, err)
	}
}

// A database written by Libre WT 0.1 is upgraded in place: the old audit
// records stay, new records can name an instance, and the log can be
// filtered by instance. A database from a newer Libre WT is refused.
// Verifies: WT-SRS-011, WT-SRS-015
func TestUpgradeFrom01Database(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "wt.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	// The schema of Libre WT 0.1.0, as released.
	if _, err := db.ExecContext(ctx, `
CREATE TABLE users (id INTEGER PRIMARY KEY AUTOINCREMENT, username TEXT NOT NULL UNIQUE, pw_hash TEXT NOT NULL, role TEXT NOT NULL,
	disabled INTEGER NOT NULL DEFAULT 0, epoch INTEGER NOT NULL DEFAULT 1, created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);
CREATE TABLE audit (id INTEGER PRIMARY KEY AUTOINCREMENT, at INTEGER NOT NULL, user TEXT NOT NULL DEFAULT '', ip TEXT NOT NULL DEFAULT '',
	action TEXT NOT NULL, target TEXT NOT NULL DEFAULT '', outcome TEXT NOT NULL, detail TEXT NOT NULL DEFAULT '');
CREATE INDEX idx_audit_at ON audit(at);
INSERT INTO audit (at, user, ip, action, target, outcome, detail) VALUES (1000, 'admin1', '127.0.0.1', 'exception.resolve', 'c1', 'success', 'old record');`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	for range 2 { // the upgrade can be repeated
		s, err := Open(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		_ = s.Close()
	}
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.Audit(ctx, Entry{User: "admin1", Instance: "dcm-1", Action: "exception.resolve", Target: "c2", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Audit(ctx, Entry{User: "admin1", Instance: "dcm-2", Action: "exception.review", Target: "c3", Outcome: "success"}); err != nil {
		t.Fatal(err)
	}
	all, _ := s.AuditLog(ctx, 0)
	if len(all) != 3 || all[2].Detail != "old record" || all[2].Instance != "" || all[0].Instance != "dcm-2" {
		t.Fatalf("all = %+v", all)
	}
	one, err := s.AuditLogFor(ctx, "dcm-1", 0)
	if err != nil || len(one) != 1 || one[0].Target != "c2" || one[0].Instance != "dcm-1" {
		t.Fatalf("filtered = %+v, %v", one, err)
	}
	if none, _ := s.AuditLogFor(ctx, "dcm-9", 0); len(none) != 0 {
		t.Fatalf("unknown instance = %+v", none)
	}
	if _, err := s.RememberIdentity(ctx, "dcm-1", "node-1", "AE1"); err != nil {
		t.Fatalf("identity table missing after upgrade: %v", err)
	}
	_ = s.Close()
	// Going back: the statements Libre WT 0.1 uses still work on the
	// upgraded database (0.1 does not name the new column).
	db, _ = sql.Open("sqlite", "file:"+path)
	if _, err := db.ExecContext(ctx, `INSERT INTO audit (at, user, ip, action, target, outcome, detail) VALUES (?,?,?,?,?,?,?)`,
		2000, "admin1", "127.0.0.1", "login", "admin1", "success", "written by 0.1"); err != nil {
		t.Fatalf("0.1 audit insert on the upgraded database: %v", err)
	}
	var n int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM (SELECT id, at, user, ip, action, target, outcome, detail FROM audit ORDER BY id DESC LIMIT 500)`).Scan(&n); err != nil || n != 4 {
		t.Fatalf("0.1 audit read on the upgraded database: n=%d err=%v", n, err)
	}
	_, _ = db.ExecContext(ctx, `PRAGMA user_version = 99`)
	_ = db.Close()
	if _, err := Open(ctx, path); err == nil || !strings.Contains(err.Error(), "newer Libre WT") {
		t.Fatalf("newer schema opened: %v", err)
	}
}

// The identity an instance reported first is kept: a second report never
// replaces it. Only an acceptance does, and it records who and why.
// Verifies: WT-SRS-015
func TestInstanceIdentityStore(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if _, ok, err := s.InstanceIdentity(ctx, "dcm-1"); ok || err != nil {
		t.Fatalf("fresh: ok=%v err=%v", ok, err)
	}
	id, err := s.RememberIdentity(ctx, "dcm-1", "node-1", "AE1")
	if err != nil || id.NodeID != "node-1" || id.AETitle != "AE1" || id.FirstSeen.IsZero() || id.AcceptedBy != "" {
		t.Fatalf("remember = %+v, %v", id, err)
	}
	id, err = s.RememberIdentity(ctx, "dcm-1", "node-EVIL", "AE9")
	if err != nil || id.NodeID != "node-1" || id.AETitle != "AE1" {
		t.Fatalf("second report replaced the identity: %+v, %v", id, err)
	}
	// Completing only fills an empty AE title of the same node.
	if err := s.CompleteIdentity(ctx, "dcm-1", "node-1", "AE-NEW"); err != nil {
		t.Fatal(err)
	}
	if id, _, _ = s.InstanceIdentity(ctx, "dcm-1"); id.AETitle != "AE1" {
		t.Fatalf("complete replaced an AE title: %+v", id)
	}
	_, _ = s.RememberIdentity(ctx, "old", "node-7", "")
	_ = s.CompleteIdentity(ctx, "old", "node-OTHER", "AE7")
	if id, _, _ = s.InstanceIdentity(ctx, "old"); id.AETitle != "" {
		t.Fatalf("complete matched another node: %+v", id)
	}
	_ = s.CompleteIdentity(ctx, "old", "node-7", "AE7")
	if id, _, _ = s.InstanceIdentity(ctx, "old"); id.AETitle != "AE7" || id.NodeID != "node-7" {
		t.Fatalf("complete = %+v", id)
	}
	if err := s.AcceptIdentity(ctx, "dcm-1", "node-2", "AE2", "admin1", "node replaced"); err != nil {
		t.Fatal(err)
	}
	id, ok, _ := s.InstanceIdentity(ctx, "dcm-1")
	if !ok || id.NodeID != "node-2" || id.AETitle != "AE2" || id.AcceptedBy != "admin1" || id.Reason != "node replaced" || id.AcceptedAt.IsZero() {
		t.Fatalf("accepted = %+v", id)
	}
	if other, _, _ := s.InstanceIdentity(ctx, "old"); other.NodeID != "node-7" {
		t.Fatalf("accept touched another instance: %+v", other)
	}
}
