// Package fleet is Libre WT's registry of LibreDCM instances (WT-SI-05): the
// named connections from the configuration file, a short status cache shared
// by all users, the identity each instance reported first, and the control
// functions each instance supports (WT-SRS-013…018, -023, -036).
//
// The registry only reads. It never changes anything on a LibreDCM and it
// never reads in the background: a status read happens when a page needs it
// and is kept for a few seconds, so many open consoles do not load a node
// (WT-HAZ-16).
package fleet

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/mulcahyio/libre-rws/modules/wt/internal/accounts"
	"github.com/mulcahyio/libre-rws/modules/wt/internal/dcmclient"
)

// Client is the part of the LibreDCM control-API client Libre WT uses.
type Client interface {
	URL() string
	PlainHTTPRemote() bool
	Discovery(ctx context.Context) (dcmclient.Discovery, error)
	Status(ctx context.Context) (dcmclient.Status, error)
	Version(ctx context.Context) (map[string]any, error)
	Conflicts(ctx context.Context, state string) ([]dcmclient.Conflict, error)
	Conflict(ctx context.Context, id string) (dcmclient.Detail, error)
	Plan(ctx context.Context, id, action string, exp dcmclient.Identity) (dcmclient.Plan, error)
	Resolve(ctx context.Context, id, action, operator, reason string, exp dcmclient.Identity) (dcmclient.Result, error)

	Config(ctx context.Context) (dcmclient.ConfigView, error)
	RemoteAEs(ctx context.Context) (dcmclient.RemoteAEList, error)
	AllowList(ctx context.Context) (dcmclient.AllowList, error)
	ConfigHistory(ctx context.Context) (dcmclient.ConfigHistory, error)
	ReloadPlan(ctx context.Context, exp dcmclient.Identity) (dcmclient.ChangePlan, error)
	Reload(ctx context.Context, digest string, acks []string, env dcmclient.Envelope) (dcmclient.ChangeResult, error)
	ConfigPlan(ctx context.Context, edit dcmclient.ConfigEdit, exp dcmclient.Identity) (dcmclient.ChangePlan, error)
	ConfigApply(ctx context.Context, edit dcmclient.ConfigEdit, digest string, acks []string, env dcmclient.Envelope) (dcmclient.ChangeResult, error)
	RollbackPlan(ctx context.Context, exp dcmclient.Identity) (dcmclient.ChangePlan, *dcmclient.OverlayVersion, error)
	Rollback(ctx context.Context, digest string, acks []string, env dcmclient.Envelope) (dcmclient.ChangeResult, error)
	Echo(ctx context.Context, t dcmclient.EchoTarget, env dcmclient.Envelope) (dcmclient.EchoResult, error)

	License(ctx context.Context) (dcmclient.LicenseView, error)
	LicenseAck(ctx context.Context, env dcmclient.Envelope) (dcmclient.LicenseResult, error)
	LicenseRequest(ctx context.Context, key, site string, env dcmclient.Envelope) (dcmclient.LicenseRequestResult, error)
	LicenseInstall(ctx context.Context, text string, env dcmclient.Envelope) (dcmclient.LicenseResult, error)
	LicenseActivate(ctx context.Context, key, site string, env dcmclient.Envelope) (dcmclient.LicenseResult, error)
	Backups(ctx context.Context) (dcmclient.BackupList, error)
	Backup(ctx context.Context, env dcmclient.Envelope) (dcmclient.BackupResult, error)
}

// IdentityStore remembers the identity of each instance (the accounts
// database implements it).
type IdentityStore interface {
	InstanceIdentity(ctx context.Context, name string) (accounts.Identity, bool, error)
	RememberIdentity(ctx context.Context, name, nodeID, aeTitle string) (accounts.Identity, error)
	CompleteIdentity(ctx context.Context, name, nodeID, aeTitle string) error
	AcceptIdentity(ctx context.Context, name, nodeID, aeTitle, by, reason string) error
}

// InstanceConfig is one connection in the configuration file.
type InstanceConfig struct {
	Name      string `yaml:"name"`
	Cluster   string `yaml:"cluster"`
	URL       string `yaml:"url"`
	Token     string `yaml:"token"`
	TokenFile string `yaml:"token_file"`
	CAFile    string `yaml:"ca_file"`
}

// MaxInstances bounds the fleet: the fleet page reads every instance.
const MaxInstances = 64

var (
	nameRE    = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	clusterRE = regexp.MustCompile(`^[A-Za-z0-9 ._-]{0,64}$`)
)

// Validate checks the connection list without opening anything: unique
// names (compared without case, so "DCM-1" and "dcm-1" can't both exist),
// names of letters, digits, '-' and '_' only (they are part of page
// addresses), a token for every instance (WT-SRS-013).
func Validate(cfgs []InstanceConfig) error {
	if len(cfgs) == 0 {
		return errors.New("fleet: no LibreDCM instance is configured")
	}
	if len(cfgs) > MaxInstances {
		return fmt.Errorf("fleet: %d instances configured, at most %d are supported", len(cfgs), MaxInstances)
	}
	seen := map[string]string{}
	for i, c := range cfgs {
		if !nameRE.MatchString(c.Name) {
			return fmt.Errorf("fleet: instance %d: name %q is not allowed: use 1 to 64 letters, digits, '-' or '_'", i+1, c.Name)
		}
		key := strings.ToLower(c.Name)
		if other, dup := seen[key]; dup {
			return fmt.Errorf("fleet: two instances are named %q and %q: names must be unique (upper and lower case count as the same)", other, c.Name)
		}
		seen[key] = c.Name
		if !clusterRE.MatchString(c.Cluster) {
			return fmt.Errorf("fleet: instance %q: cluster label %q is not allowed: use up to 64 letters, digits, spaces, '.', '-' or '_'", c.Name, c.Cluster)
		}
		if strings.TrimSpace(c.Token) == "" && c.TokenFile == "" {
			return fmt.Errorf("fleet: instance %q has no admin token: set token_file (or token)", c.Name)
		}
		if c.URL == "" {
			return fmt.Errorf("fleet: instance %q has no url", c.Name)
		}
	}
	return nil
}

// Connect validates the connection list and builds one client per instance,
// each with its own token.
func Connect(cfgs []InstanceConfig) ([]Member, error) {
	if err := Validate(cfgs); err != nil {
		return nil, err
	}
	out := make([]Member, 0, len(cfgs))
	for _, c := range cfgs {
		cl, err := dcmclient.New(dcmclient.Config{URL: c.URL, Token: c.Token, TokenFile: c.TokenFile, CAFile: c.CAFile})
		if err != nil {
			return nil, fmt.Errorf("fleet: instance %q: %w", c.Name, err)
		}
		out = append(out, Member{Name: c.Name, Cluster: c.Cluster, Client: cl})
	}
	return out, nil
}

// Member is one configured instance.
type Member struct {
	Name    string
	Cluster string
	Client  Client
}

// State is the state of an instance as Libre WT sees it
// (architecture.md §6).
type State string

const (
	// Unreachable: the last status read failed. No values are shown.
	Unreachable State = "unreachable"
	// Reachable: the node answered and is the node Libre WT remembers.
	Reachable State = "reachable"
	// IdentityChanged: the node answered with another node ID or AE title
	// than the remembered one. No action is sent until an Admin accepts it.
	IdentityChanged State = "identity-changed"
)

// Snapshot is what one status read found. It is a value: pages work on a
// copy. When the node could not be read, Status is nil and no old value is
// carried over; only the time of the last good read is kept (WT-SRS-016).
type Snapshot struct {
	Name      string
	Cluster   string
	URL       string
	PlainHTTP bool // plain HTTP to an address that is not loopback (WT-SRS-036)

	State  State
	ReadAt time.Time // when Libre WT made this read
	LastOK time.Time // last read that worked; zero when none since start
	Err    string    // why the read failed

	// Legacy: the node has no discovery endpoint (LibreDCM before the
	// control API extension). Only Study Exceptions work; no status.
	Legacy   bool
	Features []string
	Version  string
	Status   *dcmclient.Status

	Reported      dcmclient.Identity // what the node says it is, in this read
	Remembered    dcmclient.Identity // what Libre WT remembers for this name
	HasRemembered bool
	IdentityErr   string // the identity could not be read from or written to the database
}

// Supports reports whether the instance serves a control function.
func (s Snapshot) Supports(feature string) bool {
	for _, f := range s.Features {
		if f == feature {
			return true
		}
	}
	return false
}

// ActionsAllowed reports whether Libre WT may send a state-changing request
// to this instance, and why not: only to a node that answered and is the one
// remembered (architecture.md §6, WT-SRS-015).
func (s Snapshot) ActionsAllowed() (bool, string) {
	switch {
	case s.State == Unreachable:
		return false, "The instance can't be reached, so nothing was sent."
	case s.State == IdentityChanged:
		return false, "The node that answers at this instance's address is not the node Libre WT remembers. " +
			"Nothing is sent to it until an administrator accepts the new identity."
	case !s.HasRemembered:
		return false, "Libre WT could not record which node this instance is, so nothing was sent."
	}
	return true, ""
}

// Expect is the identity to send with a state-changing request. It is empty
// for a legacy node, which refuses fields it doesn't know.
func (s Snapshot) Expect() dcmclient.Identity {
	if s.Legacy {
		return dcmclient.Identity{}
	}
	return s.Remembered
}

type entry struct {
	Member
	mu     sync.Mutex // held during a read: concurrent page loads share one read
	snap   Snapshot
	lastOK time.Time
}

// Registry holds the configured instances.
type Registry struct {
	entries []*entry
	byName  map[string]*entry
	ids     IdentityStore
	ttl     time.Duration
	log     *slog.Logger
	now     func() time.Time
}

// DefaultCache is how long a status read is kept for all users.
const DefaultCache = 5 * time.Second

// New builds the registry. ttl is how long a status read is reused
// (DefaultCache when zero or negative).
func New(members []Member, ids IdentityStore, ttl time.Duration, log *slog.Logger) (*Registry, error) {
	if ttl <= 0 {
		ttl = DefaultCache
	}
	r := &Registry{byName: map[string]*entry{}, ids: ids, ttl: ttl, log: log, now: time.Now}
	for _, m := range members {
		if !nameRE.MatchString(m.Name) {
			return nil, fmt.Errorf("fleet: instance name %q is not allowed", m.Name)
		}
		if _, dup := r.byName[strings.ToLower(m.Name)]; dup {
			return nil, fmt.Errorf("fleet: instance name %q is used twice", m.Name)
		}
		e := &entry{Member: m}
		r.entries = append(r.entries, e)
		r.byName[strings.ToLower(m.Name)] = e
	}
	if len(r.entries) == 0 {
		return nil, errors.New("fleet: no LibreDCM instance is configured")
	}
	return r, nil
}

// Names returns the instance names in configuration order.
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.entries))
	for _, e := range r.entries {
		out = append(out, e.Name)
	}
	return out
}

// find returns the instance with exactly this name. The address of a page
// names the instance; nothing else selects one (WT-SRS-014).
func (r *Registry) find(name string) *entry {
	e := r.byName[strings.ToLower(name)]
	if e == nil || e.Name != name {
		return nil
	}
	return e
}

// Client returns the client of an instance.
func (r *Registry) Client(name string) (Client, bool) {
	e := r.find(name)
	if e == nil {
		return nil, false
	}
	return e.Client, true
}

// Snapshot returns the state of one instance, reading the node when the
// last read is older than the cache time.
func (r *Registry) Snapshot(ctx context.Context, name string) (Snapshot, bool) {
	e := r.find(name)
	if e == nil {
		return Snapshot{}, false
	}
	return r.read(ctx, e, false), true
}

// Fresh reads the node now, whatever the cache holds. Used right before an
// identity is accepted and after a node refused a request as the wrong node.
func (r *Registry) Fresh(ctx context.Context, name string) (Snapshot, bool) {
	e := r.find(name)
	if e == nil {
		return Snapshot{}, false
	}
	return r.read(ctx, e, true), true
}

// All returns the state of every instance, in configuration order. The
// instances are read at the same time, so one slow node costs one timeout.
func (r *Registry) All(ctx context.Context) []Snapshot {
	out := make([]Snapshot, len(r.entries))
	var wg sync.WaitGroup
	for i, e := range r.entries {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out[i] = r.read(ctx, e, false)
		}()
	}
	wg.Wait()
	return out
}

func (r *Registry) read(ctx context.Context, e *entry, force bool) Snapshot {
	e.mu.Lock()
	defer e.mu.Unlock()
	if !force && !e.snap.ReadAt.IsZero() && r.now().Sub(e.snap.ReadAt) < r.ttl {
		return e.snap
	}
	s := Snapshot{Name: e.Name, Cluster: e.Cluster, URL: e.Client.URL(), PlainHTTP: e.Client.PlainHTTPRemote()}
	err := r.fetch(ctx, e.Client, &s)
	s.ReadAt = r.now()
	if err != nil {
		// Never show old values as current: drop everything the node said.
		s.State, s.Err = Unreachable, err.Error()
		s.Status, s.Features, s.Version, s.Legacy, s.Reported = nil, nil, "", false, dcmclient.Identity{}
	} else {
		s.State = Reachable
		e.lastOK = s.ReadAt
	}
	s.LastOK = e.lastOK
	r.identity(ctx, &s)
	e.snap = s
	return s
}

// fetch asks the node what it supports and reads its status.
func (r *Registry) fetch(ctx context.Context, c Client, s *Snapshot) error {
	d, err := c.Discovery(ctx)
	if dcmclient.NotFound(err) {
		// No discovery endpoint: LibreDCM without the control API
		// extension. /version tells which node it is.
		v, verr := c.Version(ctx)
		if verr != nil {
			return verr
		}
		node, _ := v["node"].(string)
		if node == "" {
			return errors.New("the address answers, but not as a LibreDCM admin listener")
		}
		s.Legacy, s.Features = true, []string{"conflicts"}
		s.Version, _ = v["version"].(string)
		s.Reported = dcmclient.Identity{NodeID: node}
		return nil
	}
	if err != nil {
		return err
	}
	s.Features = append([]string(nil), d.Features...)
	sort.Strings(s.Features)
	s.Version, s.Reported = d.Instance.Version, d.Instance.Identity()
	if s.Supports("status") {
		st, err := c.Status(ctx)
		if err != nil {
			return err
		}
		s.Status = &st
		s.Version, s.Reported = st.Instance.Version, st.Instance.Identity()
	}
	if s.Reported.NodeID == "" {
		return errors.New("the node did not say which node it is")
	}
	return nil
}

// identity compares what the node reported with what Libre WT remembers for
// this instance name, and remembers the first report (WT-SRS-015).
func (r *Registry) identity(ctx context.Context, s *Snapshot) {
	rem, ok, err := r.ids.InstanceIdentity(ctx, s.Name)
	if err != nil {
		s.IdentityErr = err.Error()
		r.log.Error("instance identity can't be read", "instance", s.Name, "err", err)
		return
	}
	if s.State == Unreachable {
		s.Remembered, s.HasRemembered = dcmclient.Identity{NodeID: rem.NodeID, AETitle: rem.AETitle}, ok
		return
	}
	if !ok {
		rem, err = r.ids.RememberIdentity(ctx, s.Name, s.Reported.NodeID, s.Reported.AETitle)
		if err != nil {
			s.IdentityErr = err.Error()
			r.log.Error("instance identity can't be stored", "instance", s.Name, "err", err)
			return
		}
		r.log.Info("instance identity remembered", "instance", s.Name, "node_id", rem.NodeID, "ae_title", rem.AETitle)
	}
	if rem.AETitle == "" && s.Reported.AETitle != "" && rem.NodeID == s.Reported.NodeID {
		// The node was a legacy LibreDCM when first seen (no AE title in
		// its answers) and now reports one under the same node ID.
		if err := r.ids.CompleteIdentity(ctx, s.Name, rem.NodeID, s.Reported.AETitle); err != nil {
			s.IdentityErr = err.Error()
			r.log.Error("instance identity can't be stored", "instance", s.Name, "err", err)
			return
		}
		rem.AETitle = s.Reported.AETitle
		r.log.Info("instance identity completed with the AE title", "instance", s.Name, "node_id", rem.NodeID, "ae_title", rem.AETitle)
	}
	s.Remembered, s.HasRemembered = dcmclient.Identity{NodeID: rem.NodeID, AETitle: rem.AETitle}, true
	same := s.Reported.NodeID == rem.NodeID && (s.Legacy || s.Reported.AETitle == rem.AETitle)
	if !same {
		s.State = IdentityChanged
	}
}

// Accept makes the identity the node reports now the remembered one. The
// caller has checked role, confirmation, approval and reason and has written
// the audit record (WT-SRS-015, -031).
func (r *Registry) Accept(ctx context.Context, name string, id dcmclient.Identity, by, reason string) error {
	e := r.find(name)
	if e == nil {
		return errors.New("fleet: unknown instance")
	}
	if err := r.ids.AcceptIdentity(ctx, name, id.NodeID, id.AETitle, by, reason); err != nil {
		return err
	}
	e.mu.Lock()
	e.snap = Snapshot{} // next page reads again
	e.mu.Unlock()
	return nil
}

// Flags are the divergence marks of one instance inside its cluster label
// (WT-SRS-023).
type Flags struct {
	Config  bool // live settings differ from the other nodes of the cluster
	Version bool // LibreDCM version differs from the other nodes of the cluster
}

// Divergence compares the instances that share a cluster label. An instance
// is flagged when its value is not the most common one in its cluster; when
// no value is the most common one, every compared instance is flagged.
// Instances that could not be read, or that report no settings digest, are
// not compared: nothing is known about them.
func Divergence(snaps []Snapshot) map[string]Flags {
	out := map[string]Flags{}
	groups := map[string][]Snapshot{}
	for _, s := range snaps {
		if s.Cluster != "" && s.State != Unreachable {
			groups[s.Cluster] = append(groups[s.Cluster], s)
		}
	}
	for _, g := range groups {
		digest, version := map[string]string{}, map[string]string{}
		for _, s := range g {
			if s.Status != nil && s.Status.Instance.ConfigDigest != "" {
				digest[s.Name] = s.Status.Instance.ConfigDigest
			}
			if s.Version != "" {
				version[s.Name] = s.Version
			}
		}
		for name := range odd(digest) {
			f := out[name]
			f.Config = true
			out[name] = f
		}
		for name := range odd(version) {
			f := out[name]
			f.Version = true
			out[name] = f
		}
	}
	return out
}

// odd returns the keys whose value is not the single most common value.
func odd(vals map[string]string) map[string]bool {
	count := map[string]int{}
	for _, v := range vals {
		count[v]++
	}
	if len(count) < 2 {
		return nil
	}
	best, bestN, tie := "", 0, false
	for v, n := range count {
		switch {
		case n > bestN:
			best, bestN, tie = v, n, false
		case n == bestN:
			tie = true
		}
	}
	out := map[string]bool{}
	for k, v := range vals {
		if tie || v != best {
			out[k] = true
		}
	}
	return out
}
