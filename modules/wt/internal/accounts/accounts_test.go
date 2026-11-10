package accounts

import (
	"context"
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
