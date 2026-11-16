package fleet_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/accounts"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/fakedcm"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/fleet"
)

func init() { accounts.ArgonMemory, accounts.ArgonTime, accounts.ArgonThreads = 1024, 1, 1 }

func store(t *testing.T) *accounts.Store {
	t.Helper()
	s, err := accounts.Open(context.Background(), filepath.Join(t.TempDir(), "wt.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func node(t *testing.T) *fakedcm.Server {
	t.Helper()
	n := fakedcm.New()
	t.Cleanup(n.Close)
	return n
}

// registry connects the fake nodes under the given names ("name" or
// "name@cluster") through the real client.
func registry(t *testing.T, ttl time.Duration, nodes map[string]*fakedcm.Server, order ...string) (*fleet.Registry, *accounts.Store) {
	t.Helper()
	var cfgs []fleet.InstanceConfig
	for _, spec := range order {
		name, cluster, _ := strings.Cut(spec, "@")
		n := nodes[name]
		cfgs = append(cfgs, fleet.InstanceConfig{Name: name, Cluster: cluster, URL: n.URL(), Token: n.Token})
	}
	members, err := fleet.Connect(cfgs)
	if err != nil {
		t.Fatal(err)
	}
	st := store(t)
	r, err := fleet.New(members, st, ttl, quiet())
	if err != nil {
		t.Fatal(err)
	}
	return r, st
}

// The connection list is refused when two instances share a name (also when
// they differ only in case), a name has characters that are not letters,
// digits, '-' or '_', or an instance has no token. Each instance uses its own
// token; a token file works.
// Verifies: WT-SRS-013
func TestConnectionsValidated(t *testing.T) {
	ok := fleet.InstanceConfig{Name: "dcm-1", URL: "http://127.0.0.1:1", Token: "t"}
	with := func(f func(*fleet.InstanceConfig)) fleet.InstanceConfig { c := ok; f(&c); return c }
	bad := map[string][]fleet.InstanceConfig{
		"none":           {},
		"duplicate":      {ok, ok},
		"duplicate case": {ok, with(func(c *fleet.InstanceConfig) { c.Name = "DCM-1" })},
		"slash":          {with(func(c *fleet.InstanceConfig) { c.Name = "a/b" })},
		"dot":            {with(func(c *fleet.InstanceConfig) { c.Name = "a.b" })},
		"space":          {with(func(c *fleet.InstanceConfig) { c.Name = "a b" })},
		"markup":         {with(func(c *fleet.InstanceConfig) { c.Name = "<b>" })},
		"empty name":     {with(func(c *fleet.InstanceConfig) { c.Name = "" })},
		"long name":      {with(func(c *fleet.InstanceConfig) { c.Name = strings.Repeat("a", 65) })},
		"no token":       {with(func(c *fleet.InstanceConfig) { c.Token = "" })},
		"blank token":    {with(func(c *fleet.InstanceConfig) { c.Token = "  " })},
		"no url":         {with(func(c *fleet.InstanceConfig) { c.URL = "" })},
		"cluster markup": {with(func(c *fleet.InstanceConfig) { c.Cluster = "<i>" })},
	}
	for name, cfgs := range bad {
		_, err := fleet.Connect(cfgs)
		if err == nil {
			t.Errorf("%s: accepted", name)
			continue
		}
		// A missing token is reported with the instance name and the
		// setting to use.
		if strings.Contains(name, "token") && !strings.Contains(err.Error(), `instance "dcm-1" has no admin token: set token_file`) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
	// An empty token file is no token either.
	empty := filepath.Join(t.TempDir(), "empty")
	_ = os.WriteFile(empty, []byte("\n"), 0o600)
	if _, err := fleet.Connect([]fleet.InstanceConfig{with(func(c *fleet.InstanceConfig) { c.Token, c.TokenFile = "", empty })}); err == nil {
		t.Error("empty token file accepted")
	}
	// The error never repeats a token.
	_, err := fleet.Connect([]fleet.InstanceConfig{with(func(c *fleet.InstanceConfig) { c.Token = "s3cret-token-value" }), with(func(c *fleet.InstanceConfig) { c.Token = "s3cret-token-value" })}) // gitleaks:allow (synthetic test value)
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Fatalf("err = %v", err)
	}

	// Two nodes with different tokens: each connection uses its own.
	a, b := node(t), node(t)
	a.Set(func(s *fakedcm.Server) { s.Token, s.NodeID = "token-a", "node-a" })
	file := filepath.Join(t.TempDir(), "b.token")
	_ = os.WriteFile(file, []byte("token-b\n"), 0o600)
	b.Set(func(s *fakedcm.Server) { s.Token, s.NodeID = "token-b", "node-b" })
	members, err := fleet.Connect([]fleet.InstanceConfig{
		{Name: "a", Cluster: "site 1", URL: a.URL(), Token: "token-a"},
		{Name: "b", URL: b.URL(), TokenFile: file},
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := fleet.New(members, store(t), time.Minute, quiet())
	if err != nil {
		t.Fatal(err)
	}
	snaps := r.All(context.Background())
	if len(snaps) != 2 || snaps[0].State != fleet.Reachable || snaps[0].Reported.NodeID != "node-a" || snaps[0].Cluster != "site 1" ||
		snaps[1].State != fleet.Reachable || snaps[1].Reported.NodeID != "node-b" {
		t.Fatalf("snaps = %+v", snaps)
	}
	// A wrong token for one node does not reach the other one's data.
	a.Set(func(s *fakedcm.Server) { s.Token = "rotated" })
	if s, _ := r.Fresh(context.Background(), "a"); s.State != fleet.Unreachable || !strings.Contains(s.Err, "401") {
		t.Fatalf("after token rotation: %+v", s)
	}
	// A name is matched exactly: no instance is picked by a near match.
	for _, n := range []string{"A", "a ", "", "c"} {
		if _, ok := r.Snapshot(context.Background(), n); ok {
			t.Errorf("name %q matched an instance", n)
		}
	}
}

// Status is read when asked and reused for the cache time, for all callers:
// many page loads cause one read. After the cache time the node is read
// again.
// Verifies: WT-SRS-016
func TestStatusReadIsCachedAndShared(t *testing.T) {
	n := node(t)
	r, _ := registry(t, time.Hour, map[string]*fakedcm.Server{"a": n}, "a")
	ctx := context.Background()
	var wg sync.WaitGroup
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if s, ok := r.Snapshot(ctx, "a"); !ok || s.State != fleet.Reachable || s.Status == nil {
				t.Errorf("snapshot = %+v", s)
			}
		}()
	}
	wg.Wait()
	r.All(ctx)
	if got := n.Hits("/control/v1/status"); got != 1 {
		t.Fatalf("status reads = %d, want 1 for 21 page loads inside the cache time", got)
	}
	// Without a cache every page load reads.
	r2, _ := registry(t, time.Nanosecond, map[string]*fakedcm.Server{"a": n}, "a")
	for range 3 {
		time.Sleep(time.Millisecond)
		r2.Snapshot(ctx, "a")
	}
	if got := n.Hits("/control/v1/status"); got != 4 {
		t.Fatalf("status reads = %d, want 4", got)
	}
}

// A node that can't be reached is shown as unreachable with the time of the
// last good read and without any of its old values.
// Verifies: WT-SRS-016
func TestUnreachableShowsNoOldValues(t *testing.T) {
	n := node(t)
	r, _ := registry(t, time.Nanosecond, map[string]*fakedcm.Server{"a": n}, "a")
	ctx := context.Background()
	before := time.Now()
	s, _ := r.Snapshot(ctx, "a")
	if s.State != fleet.Reachable || s.Status == nil || s.Status.Counters.CStore["success"] != 2040 || s.Version != "0.3.0-test" || s.LastOK.Before(before) {
		t.Fatalf("up: %+v", s)
	}
	lastOK := s.LastOK
	n.Set(func(f *fakedcm.Server) { f.Down = true })
	time.Sleep(2 * time.Millisecond)
	s, _ = r.Snapshot(ctx, "a")
	if s.State != fleet.Unreachable || s.Err == "" {
		t.Fatalf("down: state=%s err=%q", s.State, s.Err)
	}
	if s.Status != nil || s.Version != "" || len(s.Features) != 0 || !s.Reported.Zero() {
		t.Fatalf("old values kept for an unreachable node: %+v", s)
	}
	if !s.LastOK.Equal(lastOK) || !s.ReadAt.After(lastOK) {
		t.Fatalf("last contact = %v (was %v), read at %v", s.LastOK, lastOK, s.ReadAt)
	}
	// The remembered identity stays, so the banner still names the node.
	if !s.HasRemembered || s.Remembered.NodeID != "node-1" {
		t.Fatalf("remembered = %+v", s.Remembered)
	}
	if ok, why := s.ActionsAllowed(); ok || why == "" {
		t.Fatal("actions allowed on an unreachable node")
	}
	// The node answers the discovery but not the status read: still
	// unreachable, and nothing the discovery said is shown.
	n.Set(func(f *fakedcm.Server) { f.Down, f.StatusFails = false, true })
	time.Sleep(2 * time.Millisecond)
	s, _ = r.Snapshot(ctx, "a")
	if s.State != fleet.Unreachable || !strings.Contains(s.Err, "HTTP 500") || s.Status != nil || s.Version != "" || len(s.Features) != 0 || !s.Reported.Zero() || !s.LastOK.Equal(lastOK) {
		t.Fatalf("status read failed: %+v", s)
	}
	n.Set(func(f *fakedcm.Server) { f.StatusFails = false })
	// A node that never answered has no last contact.
	never := node(t)
	never.Set(func(f *fakedcm.Server) { f.Down = true })
	r2, _ := registry(t, time.Minute, map[string]*fakedcm.Server{"x": never}, "x")
	if s, _ := r2.Snapshot(ctx, "x"); s.State != fleet.Unreachable || !s.LastOK.IsZero() || s.HasRemembered {
		t.Fatalf("never reached: %+v", s)
	}
	// An address that answers but is no LibreDCM is unreachable, not "legacy".
	n.Set(func(f *fakedcm.Server) { f.Down, f.Legacy, f.NodeID = false, true, "" })
	time.Sleep(2 * time.Millisecond)
	if s, _ := r.Snapshot(ctx, "a"); s.State != fleet.Unreachable {
		t.Fatalf("node without identity: %+v", s)
	}
}

// Libre WT remembers the node ID and AE title an instance reported first. A
// change of either is flagged and blocks actions until it is accepted; the
// first identity is never replaced silently. Actions carry the remembered
// identity.
// Verifies: WT-SRS-015
func TestIdentityRememberedAndChangeDetected(t *testing.T) {
	n := node(t)
	r, st := registry(t, time.Nanosecond, map[string]*fakedcm.Server{"a": n}, "a")
	ctx := context.Background()
	s, _ := r.Snapshot(ctx, "a")
	want := dcmclient.Identity{NodeID: "node-1", AETitle: "FAKEDCM"}
	if s.State != fleet.Reachable || !s.HasRemembered || s.Remembered != want || s.Expect() != want {
		t.Fatalf("first read: %+v", s)
	}
	if ok, _ := s.ActionsAllowed(); !ok {
		t.Fatal("actions blocked on a known node")
	}
	for _, change := range []func(*fakedcm.Server){
		func(f *fakedcm.Server) { f.NodeID = "node-2" },
		func(f *fakedcm.Server) { f.NodeID, f.AETitle = "node-1", "OTHER" },
	} {
		n.Set(change)
		time.Sleep(2 * time.Millisecond)
		s, _ = r.Snapshot(ctx, "a")
		if s.State != fleet.IdentityChanged || s.Remembered != want || s.Expect() != want {
			t.Fatalf("after change: state=%s remembered=%+v reported=%+v", s.State, s.Remembered, s.Reported)
		}
		if ok, why := s.ActionsAllowed(); ok || !strings.Contains(why, "accepts the new identity") {
			t.Fatalf("actions allowed after an identity change (%q)", why)
		}
	}
	// Reading again (also after a restart of Libre WT: a new registry on
	// the same database) does not adopt the new identity.
	members, _ := fleet.Connect([]fleet.InstanceConfig{{Name: "a", URL: n.URL(), Token: n.Token}})
	r2, _ := fleet.New(members, st, time.Nanosecond, quiet())
	if s, _ := r2.Snapshot(ctx, "a"); s.State != fleet.IdentityChanged || s.Remembered != want {
		t.Fatalf("after restart: %+v", s)
	}
	// Accepting makes the reported identity the expected one.
	if err := r.Accept(ctx, "a", s.Reported, "admin1", "node replaced"); err != nil {
		t.Fatal(err)
	}
	s, _ = r.Snapshot(ctx, "a")
	now := dcmclient.Identity{NodeID: "node-1", AETitle: "OTHER"}
	if s.State != fleet.Reachable || s.Remembered != now || s.Expect() != now {
		t.Fatalf("after accept: %+v", s)
	}
	id, _, _ := st.InstanceIdentity(ctx, "a")
	if id.AcceptedBy != "admin1" || id.Reason != "node replaced" || id.AcceptedAt.IsZero() {
		t.Fatalf("stored = %+v", id)
	}
	if err := r.Accept(ctx, "nope", now, "admin1", "x"); err == nil {
		t.Fatal("accepted for an unknown instance")
	}
}

// brokenStore is an identity store that can't be read or written.
type brokenStore struct{ fleet.IdentityStore }

func (brokenStore) InstanceIdentity(context.Context, string) (accounts.Identity, bool, error) {
	return accounts.Identity{}, false, errors.New("database is locked (test)")
}

// writeBroken can be read (nothing remembered) but not written.
type writeBroken struct{ fleet.IdentityStore }

func (writeBroken) InstanceIdentity(context.Context, string) (accounts.Identity, bool, error) {
	return accounts.Identity{}, false, nil
}

func (writeBroken) RememberIdentity(context.Context, string, string, string) (accounts.Identity, error) {
	return accounts.Identity{}, errors.New("disk full (test)")
}

// When Libre WT can't read or record which node an instance is, it sends no
// action to it: without a remembered identity nothing can be checked.
// Verifies: WT-SRS-015
func TestNoActionsWithoutRememberedIdentity(t *testing.T) {
	n := node(t)
	members, err := fleet.Connect([]fleet.InstanceConfig{{Name: "a", URL: n.URL(), Token: n.Token}})
	if err != nil {
		t.Fatal(err)
	}
	for name, ids := range map[string]fleet.IdentityStore{"read fails": brokenStore{}, "write fails": writeBroken{}} {
		r, err := fleet.New(members, ids, time.Nanosecond, quiet())
		if err != nil {
			t.Fatal(err)
		}
		s, _ := r.Snapshot(context.Background(), "a")
		if s.State != fleet.Reachable || s.Status == nil || s.HasRemembered || s.IdentityErr == "" {
			t.Fatalf("%s: %+v", name, s)
		}
		if ok, why := s.ActionsAllowed(); ok || !strings.Contains(why, "could not record which node") {
			t.Fatalf("%s: actions allowed without a remembered identity (%q)", name, why)
		}
	}
}

// An instance says which control functions it supports. A LibreDCM without
// the discovery endpoint is handled as a node that supports Study Exceptions
// only: no status values, no error, and requests to it carry no expected
// identity (it would refuse the unknown fields).
// Verifies: WT-SRS-018
func TestFeaturesAndLegacyNode(t *testing.T) {
	n, old := node(t), node(t)
	n.Set(func(f *fakedcm.Server) { f.Features = []string{"conflicts", "status", "echo"} })
	old.Set(func(f *fakedcm.Server) { f.Legacy, f.NodeID, f.Version = true, "old-1", "0.2.0" })
	r, _ := registry(t, time.Nanosecond, map[string]*fakedcm.Server{"new": n, "old": old}, "new", "old")
	ctx := context.Background()
	s, _ := r.Snapshot(ctx, "new")
	if !s.Supports("status") || !s.Supports("echo") || s.Supports("backup") || s.Supports("") || s.Legacy {
		t.Fatalf("features = %v", s.Features)
	}
	o, _ := r.Snapshot(ctx, "old")
	if o.State != fleet.Reachable || !o.Legacy || o.Status != nil || !o.Supports("conflicts") || o.Supports("status") || o.Version != "0.2.0" {
		t.Fatalf("legacy = %+v", o)
	}
	if o.Remembered.NodeID != "old-1" || !o.Expect().Zero() {
		t.Fatalf("legacy identity = %+v expect = %+v", o.Remembered, o.Expect())
	}
	if ok, _ := o.ActionsAllowed(); !ok {
		t.Fatal("legacy node blocked")
	}
	// A node that lists no status function is not asked for it.
	n.Set(func(f *fakedcm.Server) { f.Features = []string{"conflicts"} })
	before := n.Hits("/control/v1/status")
	time.Sleep(2 * time.Millisecond)
	if s, _ = r.Snapshot(ctx, "new"); s.Status != nil || s.State != fleet.Reachable || n.Hits("/control/v1/status") != before {
		t.Fatalf("status asked from a node that does not list it: %+v", s)
	}
	// The legacy node is upgraded: same node ID, now with an AE title. The
	// AE title is added to what is remembered; another node ID is a change.
	old.Set(func(f *fakedcm.Server) { f.Legacy, f.AETitle = false, "ARCHIVE" })
	time.Sleep(2 * time.Millisecond)
	if o, _ = r.Snapshot(ctx, "old"); o.State != fleet.Reachable || o.Remembered != (dcmclient.Identity{NodeID: "old-1", AETitle: "ARCHIVE"}) || o.Expect().AETitle != "ARCHIVE" {
		t.Fatalf("after upgrade: %+v", o)
	}
	old.Set(func(f *fakedcm.Server) { f.AETitle = "CHANGED" })
	time.Sleep(2 * time.Millisecond)
	if o, _ = r.Snapshot(ctx, "old"); o.State != fleet.IdentityChanged {
		t.Fatalf("AE title change after upgrade not flagged: %+v", o)
	}
}

// Within one cluster label, instances whose settings digest or version
// differs are flagged. Instances without a label, or that can't be read, are
// not compared.
// Verifies: WT-SRS-023
func TestDivergenceWithinCluster(t *testing.T) {
	mk := func(name, cluster, digest, version string, state fleet.State) fleet.Snapshot {
		s := fleet.Snapshot{Name: name, Cluster: cluster, State: state, Version: version}
		if state != fleet.Unreachable {
			s.Status = &dcmclient.Status{}
			s.Status.Instance.ConfigDigest = digest
		}
		return s
	}
	R, U := fleet.Reachable, fleet.Unreachable
	got := fleet.Divergence([]fleet.Snapshot{
		mk("a1", "site-a", "d1", "0.3.0", R), mk("a2", "site-a", "d1", "0.3.0", R), mk("a3", "site-a", "d2", "0.3.1", R),
		mk("a4", "site-a", "", "", U),
		mk("b1", "site-b", "d1", "0.3.0", R), mk("b2", "site-b", "d9", "0.3.0", R),
		mk("c1", "site-c", "d1", "0.3.0", R), mk("c2", "site-c", "d1", "0.3.0", fleet.IdentityChanged),
		mk("s1", "", "d5", "0.1.0", R), mk("s2", "", "d6", "0.2.0", R),
	})
	want := map[string]fleet.Flags{
		"a3": {Config: true, Version: true}, // the odd one out of three
		"b1": {Config: true},                // one against one: both flagged
		"b2": {Config: true},
	}
	if len(got) != len(want) {
		t.Fatalf("flags = %+v", got)
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("%s = %+v, want %+v", k, got[k], v)
		}
	}
	// End to end through the registry and the real client.
	x, y := node(t), node(t)
	y.Set(func(f *fakedcm.Server) { f.NodeID, f.Digest = "node-2", "sha256:bbbb" })
	r, _ := registry(t, time.Nanosecond, map[string]*fakedcm.Server{"x": x, "y": y}, "x@site", "y@site")
	flags := fleet.Divergence(r.All(context.Background()))
	if !flags["x"].Config || !flags["y"].Config || flags["x"].Version {
		t.Fatalf("flags = %+v", flags)
	}
	y.Set(func(f *fakedcm.Server) { f.Digest = "sha256:aaaa" })
	time.Sleep(2 * time.Millisecond)
	if flags := fleet.Divergence(r.All(context.Background())); len(flags) != 0 {
		t.Fatalf("equal nodes flagged: %+v", flags)
	}
}
