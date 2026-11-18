package dicomuid

import (
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

// Site numbers (uid-registry.md §4). Configured sites come from the
// registry's allocation log; a node without one picks a random site from a
// range that can never hold a configured one.
const (
	MaxConfiguredSite = 999_999   // configured sites: 1..999999
	minRandomSite     = 1_000_000 // random sites: 1000000..2^40-1
	maxRandomSite     = 1<<40 - 1 // 13 digits
	maxCounter        = 999_999   // per-microsecond counter: 0..999999
	reserveAheadMicro = 1_000_000 // state file reserves 1 s of time ahead
	stateFileMode     = os.FileMode(0o600)
)

// Options configure a Generator.
type Options struct {
	// Product is the generating product (required).
	Product Product
	// Site is the site number from the UID registry's allocation log
	// (1..999999). 0 means not configured: the generator uses the random
	// site kept in StateFile, or picks a new random one.
	Site uint64
	// StateFile, when set, keeps the random site and a time high-water mark
	// across restarts, so UIDs stay unique and increasing even if the clock
	// is set back. Without it, uniqueness across restarts relies on a new
	// random site (or, for a configured site, on the clock).
	StateFile string
	// Now is the clock; nil means time.Now. Tests only.
	Now func() time.Time
}

// Generator makes UIDs 1.3.6.1.4.1.67686.2.<product>.<site>.<time>.<counter>
// where <time> is microseconds since the Unix epoch and <counter> counts
// UIDs within one microsecond. Each (time, counter) pair is issued at most
// once and pairs strictly increase, also when the clock steps back. It is
// safe for concurrent use.
type Generator struct {
	mu       sync.Mutex
	prefix   string
	site     uint64
	state    string
	now      func() time.Time
	last     int64 // time of the last UID issued
	counter  int64 // counter of the last UID issued
	reserved int64 // time up to which the state file allows issuing
}

type stateFile struct {
	Site            uint64 `json:"site"`
	ReservedUntilUS int64  `json:"reserved_until_us"`
}

// ErrState is returned when the state file cannot be read or written. The
// generator then refuses to issue UIDs rather than risk a repeat.
var ErrState = errors.New("dicomuid: generator state")

// NewGenerator returns a generator for o.
func NewGenerator(o Options) (*Generator, error) {
	if !o.Product.Valid() {
		return nil, fmt.Errorf("dicomuid: product %d is not allocated", o.Product)
	}
	if o.Site > MaxConfiguredSite {
		return nil, fmt.Errorf("dicomuid: configured site %d is out of range 1..%d", o.Site, MaxConfiguredSite)
	}
	g := &Generator{state: o.StateFile, now: o.Now}
	if g.now == nil {
		g.now = time.Now
	}
	var st stateFile
	if o.StateFile != "" {
		b, err := os.ReadFile(o.StateFile)
		switch {
		case err == nil:
			if err := json.Unmarshal(b, &st); err != nil || st.ReservedUntilUS < 0 ||
				(st.Site != 0 && (st.Site < minRandomSite || st.Site > maxRandomSite)) {
				return nil, fmt.Errorf("%w: %s is damaged; remove it only after checking the clock and site", ErrState, o.StateFile)
			}
		case errors.Is(err, os.ErrNotExist):
		default:
			return nil, fmt.Errorf("%w: %v", ErrState, err)
		}
	}
	g.site = o.Site
	if g.site == 0 {
		g.site = st.Site
	}
	if g.site == 0 {
		n, err := rand.Int(rand.Reader, big.NewInt(maxRandomSite-minRandomSite+1))
		if err != nil {
			return nil, fmt.Errorf("dicomuid: random site: %w", err)
		}
		g.site = n.Uint64() + minRandomSite
	}
	if o.Site == 0 {
		st.Site = g.site // keep the random site; a configured one lives in config
	} else {
		st.Site = 0
	}
	// Start just after anything a previous run may have issued: the next UID
	// moves to time reserved+1 (or later, by the clock).
	g.last, g.counter, g.reserved = st.ReservedUntilUS, maxCounter, st.ReservedUntilUS
	if g.state != "" {
		if err := g.persist(st.Site, g.reserved); err != nil {
			return nil, err
		}
	}
	g.prefix = fmt.Sprintf("%s.%d.%d.", GeneratedArc, o.Product, g.site)
	return g, nil
}

// Site returns the site number in use.
func (g *Generator) Site() uint64 { return g.site }

// New returns a new UID.
func (g *Generator) New() (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	t, c := g.last, g.counter+1
	if now := g.now().UnixMicro(); now > t {
		t, c = now, 0
	} else if c > maxCounter {
		t, c = t+1, 0 // borrow from the future; the clock catches up
	}
	if g.state != "" && t > g.reserved {
		r := t + reserveAheadMicro
		var site uint64
		if g.site >= minRandomSite {
			site = g.site
		}
		if err := g.persist(site, r); err != nil {
			return "", err
		}
		g.reserved = r
	}
	uid := g.prefix + strconv.FormatInt(t, 10) + "." + strconv.FormatInt(c, 10)
	if err := Valid(uid); err != nil {
		return "", err
	}
	g.last, g.counter = t, c
	return uid, nil
}

// persist writes the state atomically (temporary file, fsync, rename).
func (g *Generator) persist(site uint64, reserved int64) error {
	b, err := json.Marshal(stateFile{Site: site, ReservedUntilUS: reserved})
	if err != nil {
		return fmt.Errorf("%w: %v", ErrState, err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(g.state), ".dicomuid-*")
	if err != nil {
		return fmt.Errorf("%w: %v", ErrState, err)
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(stateFileMode); err != nil {
		return fmt.Errorf("%w: %v", ErrState, err)
	}
	if _, err := tmp.Write(b); err != nil {
		return fmt.Errorf("%w: %v", ErrState, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("%w: %v", ErrState, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("%w: %v", ErrState, err)
	}
	if err := os.Rename(name, g.state); err != nil {
		return fmt.Errorf("%w: %v", ErrState, err)
	}
	ok = true
	if d, err := os.Open(filepath.Dir(g.state)); err == nil {
		_ = d.Sync() // make the rename durable where the OS supports it
		_ = d.Close()
	}
	return nil
}
