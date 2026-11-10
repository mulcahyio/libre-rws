// Package accounts holds Libre WT's local user accounts, roles and audit log
// in a SQLite database inside Libre WT's own data directory (WT-SRS-002…005,
// WT-SRS-011). Passwords are stored as argon2id hashes.
package accounts

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/argon2"
	_ "modernc.org/sqlite" // driver "sqlite"
)

// Role is a user's role. Viewer and Operator can see the Study Exceptions
// queue; only Admin can resolve exceptions, manage users and read the audit
// log. Operator will gain service control in a later version.
type Role string

const (
	Viewer   Role = "viewer"
	Operator Role = "operator"
	Admin    Role = "admin"
)

// ValidRole reports whether r is a known role.
func ValidRole(r Role) bool { return r == Viewer || r == Operator || r == Admin }

// User is an account. Epoch changes whenever the role, password or disabled
// flag changes; sessions remember the epoch they were created with and end
// when it no longer matches (WT-SRS-005).
type User struct {
	ID        int64
	Username  string
	Role      Role
	Disabled  bool
	Epoch     int64
	CreatedAt time.Time
}

// Entry is one audit record.
type Entry struct {
	ID      int64
	At      time.Time
	User    string
	IP      string
	Action  string
	Target  string
	Outcome string // success | failure
	Detail  string
}

// Errors.
var (
	ErrBadCredentials = errors.New("invalid username or password")
	ErrNotFound       = errors.New("user not found")
	ErrExists         = errors.New("username already exists")
	ErrLastAdmin      = errors.New("the last active administrator can't be disabled or demoted")
	ErrSetupDone      = errors.New("setup already completed")
)

// MinPasswordLength is the minimum password length (WT-SRS-003).
const MinPasswordLength = 12

var usernameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// Argon2id parameters (OWASP: at least m=19 MiB, t=2, p=1). Tests lower them.
var (
	ArgonMemory  uint32 = 64 * 1024 // KiB
	ArgonTime    uint32 = 3
	ArgonThreads uint8  = 2
)

const saltLen, keyLen = 16, 32

// Store is the account database.
type Store struct {
	db  *sql.DB
	now func() time.Time
	// dummy is a valid hash used when the username doesn't exist, so a
	// failed login takes the same time either way.
	dummy string
}

const schema = `
CREATE TABLE IF NOT EXISTS users (
	id         INTEGER PRIMARY KEY AUTOINCREMENT,
	username   TEXT NOT NULL UNIQUE,
	pw_hash    TEXT NOT NULL,
	role       TEXT NOT NULL,
	disabled   INTEGER NOT NULL DEFAULT 0,
	epoch      INTEGER NOT NULL DEFAULT 1,
	created_at INTEGER NOT NULL,
	updated_at INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS audit (
	id      INTEGER PRIMARY KEY AUTOINCREMENT,
	at      INTEGER NOT NULL,
	user    TEXT NOT NULL DEFAULT '',
	ip      TEXT NOT NULL DEFAULT '',
	action  TEXT NOT NULL,
	target  TEXT NOT NULL DEFAULT '',
	outcome TEXT NOT NULL,
	detail  TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_audit_at ON audit(at);
`

// Open opens (and creates) the database at path.
func Open(ctx context.Context, path string) (*Store, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)&_pragma=synchronous(FULL)")
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("accounts: schema: %w", err)
	}
	s := &Store{db: db, now: time.Now}
	s.dummy = hashPassword("dummy-password-for-timing")
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

func hashPassword(pw string) string {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		panic(err) // crypto/rand doesn't fail on supported platforms
	}
	key := argon2.IDKey([]byte(pw), salt, ArgonTime, ArgonMemory, ArgonThreads, keyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version, ArgonMemory, ArgonTime, ArgonThreads,
		b64.EncodeToString(salt), b64.EncodeToString(key))
}

func verifyPassword(encoded, pw string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var v int
	var m, t uint32
	var p uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &v); err != nil || v != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &m, &t, &p); err != nil {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(pw), salt, t, m, p, uint32(len(want))) // #nosec G115 -- key length is 32
	return subtle.ConstantTimeCompare(got, want) == 1
}

// CheckPassword enforces the password policy.
func CheckPassword(pw, username string) error {
	if utf8.RuneCountInString(pw) < MinPasswordLength {
		return fmt.Errorf("password must be at least %d characters", MinPasswordLength)
	}
	if len(pw) > 256 {
		return errors.New("password is too long")
	}
	if strings.EqualFold(pw, username) {
		return errors.New("password must differ from the username")
	}
	return nil
}

// NormalizeUsername lower-cases and validates a username.
func NormalizeUsername(u string) (string, error) {
	u = strings.ToLower(strings.TrimSpace(u))
	if !usernameRE.MatchString(u) {
		return "", errors.New("username: 1-64 characters, letters, digits, '.', '_' or '-'")
	}
	return u, nil
}

// HasUsers reports whether any account exists (first-run setup otherwise).
func (s *Store) HasUsers(ctx context.Context) (bool, error) {
	var n int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n)
	return n > 0, err
}

// CreateFirstAdmin creates the initial administrator; it fails once any
// account exists, so the setup page can't be replayed (WT-SRS-002).
func (s *Store) CreateFirstAdmin(ctx context.Context, username, password string) (User, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return User{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var n int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users`).Scan(&n); err != nil {
		return User{}, err
	}
	if n > 0 {
		return User{}, ErrSetupDone
	}
	u, err := s.insert(ctx, tx, username, password, Admin)
	if err != nil {
		return User{}, err
	}
	return u, tx.Commit()
}

// CreateUser adds an account.
func (s *Store) CreateUser(ctx context.Context, username, password string, role Role) (User, error) {
	return s.insert(ctx, s.db, username, password, role)
}

type execer interface {
	ExecContext(ctx context.Context, q string, args ...any) (sql.Result, error)
}

func (s *Store) insert(ctx context.Context, ex execer, username, password string, role Role) (User, error) {
	username, err := NormalizeUsername(username)
	if err != nil {
		return User{}, err
	}
	if !ValidRole(role) {
		return User{}, fmt.Errorf("unknown role %q", role)
	}
	if err := CheckPassword(password, username); err != nil {
		return User{}, err
	}
	now := s.now().UnixMilli()
	res, err := ex.ExecContext(ctx, `INSERT INTO users (username, pw_hash, role, created_at, updated_at) VALUES (?,?,?,?,?)`,
		username, hashPassword(password), string(role), now, now)
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return User{}, ErrExists
		}
		return User{}, err
	}
	id, _ := res.LastInsertId()
	return User{ID: id, Username: username, Role: role, Epoch: 1, CreatedAt: time.UnixMilli(now)}, nil
}

const userCols = `id, username, role, disabled, epoch, created_at`

func scanUser(sc interface{ Scan(...any) error }) (User, error) {
	var u User
	var role string
	var disabled int
	var created int64
	if err := sc.Scan(&u.ID, &u.Username, &role, &disabled, &u.Epoch, &created); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return u, ErrNotFound
		}
		return u, err
	}
	u.Role, u.Disabled, u.CreatedAt = Role(role), disabled == 1, time.UnixMilli(created)
	return u, nil
}

// Authenticate checks a username and password. Disabled accounts fail like
// wrong passwords.
func (s *Store) Authenticate(ctx context.Context, username, password string) (User, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	var hash string
	var u User
	var role string
	var disabled int
	var created int64
	err := s.db.QueryRowContext(ctx, `SELECT `+userCols+`, pw_hash FROM users WHERE username = ?`, username).
		Scan(&u.ID, &u.Username, &role, &disabled, &u.Epoch, &created, &hash)
	if errors.Is(err, sql.ErrNoRows) {
		verifyPassword(s.dummy, password)
		return User{}, ErrBadCredentials
	}
	if err != nil {
		return User{}, err
	}
	if !verifyPassword(hash, password) || disabled == 1 {
		return User{}, ErrBadCredentials
	}
	u.Role, u.CreatedAt = Role(role), time.UnixMilli(created)
	return u, nil
}

// Get returns a user by id.
func (s *Store) Get(ctx context.Context, id int64) (User, error) {
	return scanUser(s.db.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
}

// List returns all users.
func (s *Store) List(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+userCols+` FROM users ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []User
	for rows.Next() {
		u, err := scanUser(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	return out, rows.Err()
}

// activeAdminsExcept counts enabled admins other than id.
func (s *Store) activeAdminsExcept(ctx context.Context, tx *sql.Tx, id int64) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = 0 AND id <> ?`, id).Scan(&n)
	return n, err
}

func (s *Store) update(ctx context.Context, id int64, guardAdmin bool, query string, args ...any) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	u, err := scanUser(tx.QueryRowContext(ctx, `SELECT `+userCols+` FROM users WHERE id = ?`, id))
	if err != nil {
		return err
	}
	if guardAdmin && u.Role == Admin && !u.Disabled {
		n, err := s.activeAdminsExcept(ctx, tx, id)
		if err != nil {
			return err
		}
		if n == 0 {
			return ErrLastAdmin
		}
	}
	args = append(args, s.now().UnixMilli(), id)
	if _, err := tx.ExecContext(ctx, query, args...); err != nil {
		return err
	}
	return tx.Commit()
}

// SetRole changes a user's role (ends their sessions).
func (s *Store) SetRole(ctx context.Context, id int64, role Role) error {
	if !ValidRole(role) {
		return fmt.Errorf("unknown role %q", role)
	}
	return s.update(ctx, id, role != Admin, `UPDATE users SET role = ?, epoch = epoch + 1, updated_at = ? WHERE id = ?`, string(role))
}

// SetDisabled disables or enables a user (ends their sessions).
func (s *Store) SetDisabled(ctx context.Context, id int64, disabled bool) error {
	d := 0
	if disabled {
		d = 1
	}
	return s.update(ctx, id, disabled, `UPDATE users SET disabled = ?, epoch = epoch + 1, updated_at = ? WHERE id = ?`, d)
}

// SetPassword replaces a password (ends the user's sessions).
func (s *Store) SetPassword(ctx context.Context, id int64, password string) error {
	u, err := s.Get(ctx, id)
	if err != nil {
		return err
	}
	if err := CheckPassword(password, u.Username); err != nil {
		return err
	}
	return s.update(ctx, id, false, `UPDATE users SET pw_hash = ?, epoch = epoch + 1, updated_at = ? WHERE id = ?`, hashPassword(password))
}

// Audit appends an audit record. Audit failures are returned so callers can
// refuse the action they were about to record.
func (s *Store) Audit(ctx context.Context, e Entry) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO audit (at, user, ip, action, target, outcome, detail) VALUES (?,?,?,?,?,?,?)`,
		s.now().UnixMilli(), e.User, e.IP, e.Action, e.Target, e.Outcome, e.Detail)
	return err
}

// AuditLog returns the newest records first.
func (s *Store) AuditLog(ctx context.Context, limit int) ([]Entry, error) {
	if limit <= 0 || limit > 5000 {
		limit = 500
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, user, ip, action, target, outcome, detail FROM audit ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Entry
	for rows.Next() {
		var e Entry
		var at int64
		if err := rows.Scan(&e.ID, &at, &e.User, &e.IP, &e.Action, &e.Target, &e.Outcome, &e.Detail); err != nil {
			return nil, err
		}
		e.At = time.UnixMilli(at).UTC()
		out = append(out, e)
	}
	return out, rows.Err()
}
