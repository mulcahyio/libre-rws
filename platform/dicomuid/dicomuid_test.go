package dicomuid

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// Verifies: DCM-SRS-098
func TestImplementationLayout(t *testing.T) {
	cases := []struct {
		p                  Product
		version, uid, name string
	}{
		{LibreDCM, "0.2.0", "1.3.6.1.4.1.67686.1.1.1.0.2", "LIBREDCM_0_2"},
		{LibreDCM, "v0.2.0-beta.1", "1.3.6.1.4.1.67686.1.1.1.0.2", "LIBREDCM_0_2"},
		{LibreDCM, "0.2.0-beta.1-12-gabc1234", "1.3.6.1.4.1.67686.1.1.1.0.2", "LIBREDCM_0_2"},
		{LibreDCM, "1.10", "1.3.6.1.4.1.67686.1.1.1.1.10", "LIBREDCM_1_10"},
		{LibreDCM, "999.999.0", "1.3.6.1.4.1.67686.1.1.1.999.999", "LIBREDCM_999_999"},
		{LibreDCM, "dev", "1.3.6.1.4.1.67686.1.1.1.0.0", "LIBREDCM_0_0"},
		{LibreDCM, "ci", "1.3.6.1.4.1.67686.1.1.1.0.0", "LIBREDCM_0_0"},
		{LibreDCM, "", "1.3.6.1.4.1.67686.1.1.1.0.0", "LIBREDCM_0_0"},
		{LibreDCM, "1000.1.0", "1.3.6.1.4.1.67686.1.1.1.0.0", "LIBREDCM_0_0"},
		{LibreDCM, "01.2.0", "1.3.6.1.4.1.67686.1.1.1.0.0", "LIBREDCM_0_0"},
		{LibreDCM, "0.2x", "1.3.6.1.4.1.67686.1.1.1.0.0", "LIBREDCM_0_0"},
		{LibreWT, "0.2.0", "1.3.6.1.4.1.67686.1.2.1.0.2", "LIBREWT_0_2"},
		{LibreDVT, "0.1.0", "1.3.6.1.4.1.67686.1.3.1.0.1", "LIBREDVT_0_1"},
		{LibreKey, "2.3.4", "1.3.6.1.4.1.67686.1.4.1.2.3", "LIBREKEY_2_3"},
	}
	for _, c := range cases {
		uid, name := Implementation(c.p, c.version)
		if uid != c.uid || name != c.name {
			t.Errorf("Implementation(%d, %q) = %q, %q; want %q, %q", c.p, c.version, uid, name, c.uid, c.name)
		}
		if err := Valid(uid); err != nil {
			t.Errorf("%q: %v", uid, err)
		}
		// Implementation Version Name is VR SH: at most 16 characters.
		if len(name) > 16 {
			t.Errorf("version name %q has %d characters (max 16)", name, len(name))
		}
	}
	defer func() {
		if recover() == nil {
			t.Fatal("unallocated product did not panic")
		}
	}()
	Implementation(Product(5), "0.2.0")
}

// Verifies: DCM-SRS-099
func TestValid(t *testing.T) {
	good := []string{"1", "0", "1.2", "1.3.6.1.4.1.67686.2.1.1.1795027082000000.0", "2.25.0", strings.Repeat("1.", 31) + "12"}
	for _, u := range good {
		if err := Valid(u); err != nil {
			t.Errorf("Valid(%q) = %v", u, err)
		}
	}
	bad := []string{"", ".", "1.", ".1", "1..2", "1.02", "00", "1.2a", "1.2 ", "1,2", "1.-2", strings.Repeat("1.", 32) + "1"}
	for _, u := range bad {
		if err := Valid(u); err == nil {
			t.Errorf("Valid(%q) accepted an invalid UID", u)
		}
	}
	if len(strings.Repeat("1.", 31)+"12") != 64 || len(strings.Repeat("1.", 32)+"1") != 65 {
		t.Fatal("length fixtures are wrong")
	}
}

// fakeClock is a settable clock for tests.
type fakeClock struct{ us atomic.Int64 }

func (c *fakeClock) Now() time.Time { return time.UnixMicro(c.us.Load()) }

func parse(t *testing.T, uid string) (site uint64, tm, ctr int64) {
	t.Helper()
	parts := strings.Split(strings.TrimPrefix(uid, GeneratedArc+"."), ".")
	if !strings.HasPrefix(uid, GeneratedArc+".") || len(parts) != 4 {
		t.Fatalf("UID %q is not in the generated arc layout", uid)
	}
	site, _ = strconv.ParseUint(parts[1], 10, 64)
	tm, _ = strconv.ParseInt(parts[2], 10, 64)
	ctr, _ = strconv.ParseInt(parts[3], 10, 64)
	return site, tm, ctr
}

func after(t1, c1, t0, c0 int64) bool { return t1 > t0 || (t1 == t0 && c1 > c0) }

// Verifies: DCM-SRS-099
func TestGeneratorUniqueUnderConcurrency(t *testing.T) {
	g, err := NewGenerator(Options{Product: LibreDCM, Site: 42})
	if err != nil {
		t.Fatal(err)
	}
	const workers, each = 32, 3000
	out := make([][]string, workers)
	var wg sync.WaitGroup
	for w := range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range each {
				u, err := g.New()
				if err != nil {
					t.Error(err)
					return
				}
				out[w] = append(out[w], u)
			}
		}()
	}
	wg.Wait()
	seen := make(map[string]bool, workers*each)
	for _, list := range out {
		var pt, pc int64 = -1, -1
		for _, u := range list {
			if seen[u] {
				t.Fatalf("duplicate UID %s", u)
			}
			seen[u] = true
			if err := Valid(u); err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(u, "1.3.6.1.4.1.67686.2.1.42.") {
				t.Fatalf("UID %s has the wrong prefix", u)
			}
			// Each goroutine sees strictly increasing UIDs.
			_, tm, c := parse(t, u)
			if !after(tm, c, pt, pc) {
				t.Fatalf("UID %s does not increase", u)
			}
			pt, pc = tm, c
		}
	}
	if len(seen) != workers*each {
		t.Fatalf("got %d UIDs, want %d", len(seen), workers*each)
	}
}

// Verifies: DCM-SRS-099
func TestGeneratorFrozenAndBackwardClock(t *testing.T) {
	clk := &fakeClock{}
	clk.us.Store(1_795_027_082_000_000)
	g, err := NewGenerator(Options{Product: LibreDCM, Site: 7, Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	// Strictly increasing (time, counter) pairs from one generator are unique.
	var pt, pc int64 = -1, -1
	check := func(n int) {
		for range n {
			u, err := g.New()
			if err != nil {
				t.Fatal(err)
			}
			_, tm, c := parse(t, u)
			if !after(tm, c, pt, pc) || c > maxCounter {
				t.Fatalf("UID %s does not increase or counter overflows", u)
			}
			pt, pc = tm, c
		}
	}
	// A frozen clock: the counter runs past its maximum and time is borrowed.
	check(maxCounter + 1000)
	if pt != 1_795_027_082_000_001 {
		t.Fatalf("time after counter overflow = %d", pt)
	}
	// The clock steps back one hour: UIDs keep increasing.
	clk.us.Add(-3_600_000_000)
	check(100)
	// The clock moves forward again.
	clk.us.Add(7_200_000_000)
	check(10)
	if pt != clk.us.Load() {
		t.Fatalf("generator did not follow the clock forward: %d vs %d", pt, clk.us.Load())
	}
}

// Verifies: DCM-SRS-099
func TestGeneratorMonotonicAcrossRestart(t *testing.T) {
	state := filepath.Join(t.TempDir(), "uid-state.json")
	clk := &fakeClock{}
	clk.us.Store(1_795_027_082_000_000)
	g1, err := NewGenerator(Options{Product: LibreDCM, StateFile: state, Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	site := g1.Site()
	if site < minRandomSite || site > maxRandomSite {
		t.Fatalf("random site %d outside %d..%d", site, minRandomSite, maxRandomSite)
	}
	var lastT, lastC int64
	for range 5000 {
		clk.us.Add(3) // 15 ms in total, inside the reserved second
		u, err := g1.New()
		if err != nil {
			t.Fatal(err)
		}
		_, lastT, lastC = parse(t, u)
	}
	if fi, err := os.Stat(state); err != nil || fi.Mode().Perm() != stateFileMode {
		t.Fatalf("state file: %v %v", fi, err)
	}
	// Restart with the clock set back one hour (an NTP step, a wrong RTC).
	clk.us.Add(-3_600_000_000)
	g2, err := NewGenerator(Options{Product: LibreDCM, StateFile: state, Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	if g2.Site() != site {
		t.Fatalf("random site not kept across restart: %d then %d", site, g2.Site())
	}
	u, err := g2.New()
	if err != nil {
		t.Fatal(err)
	}
	s2, t2, c2 := parse(t, u)
	if s2 != site || !after(t2, c2, lastT, lastC) {
		t.Fatalf("after restart %s is not after time %d counter %d", u, lastT, lastC)
	}
	// A configured site overrides the random one, keeps the time mark, and
	// is not written to the state file.
	g3, err := NewGenerator(Options{Product: LibreDCM, Site: 12, StateFile: state, Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	u3, err := g3.New()
	if err != nil {
		t.Fatal(err)
	}
	if s3, t3, c3 := parse(t, u3); s3 != 12 || !after(t3, c3, t2, c2) {
		t.Fatalf("configured site restart gave %s", u3)
	}
	var st stateFile
	b, _ := os.ReadFile(state)
	if err := json.Unmarshal(b, &st); err != nil || st.Site != 0 {
		t.Fatalf("state after configured site = %s (%v)", b, err)
	}
}

// Verifies: DCM-SRS-099
func TestGeneratorWithoutStateFilePicksNewRandomSite(t *testing.T) {
	sites := map[uint64]bool{}
	for range 20 {
		g, err := NewGenerator(Options{Product: LibreWT})
		if err != nil {
			t.Fatal(err)
		}
		sites[g.Site()] = true
		u, err := g.New()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(u, "1.3.6.1.4.1.67686.2.2.") {
			t.Fatalf("product arc wrong: %s", u)
		}
	}
	if len(sites) < 19 { // 20 draws from 2^40 values
		t.Fatalf("random sites repeat: %v", sites)
	}
}

// Verifies: DCM-SRS-099
func TestGeneratorFailsClosed(t *testing.T) {
	dir := t.TempDir()
	if _, err := NewGenerator(Options{Product: Product(0)}); err == nil {
		t.Error("unallocated product accepted")
	}
	if _, err := NewGenerator(Options{Product: LibreDCM, Site: MaxConfiguredSite + 1}); err == nil {
		t.Error("configured site in the random range accepted")
	}
	for name, body := range map[string]string{
		"garbage":       "not json",
		"negative time": `{"site":0,"reserved_until_us":-5}`,
		"bad site":      `{"site":12,"reserved_until_us":5}`,
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := NewGenerator(Options{Product: LibreDCM, StateFile: p}); err == nil {
			t.Errorf("%s state file accepted", name)
		}
	}
	// A state file that cannot be written: refuse to start.
	if _, err := NewGenerator(Options{Product: LibreDCM, StateFile: filepath.Join(dir, "missing", "s.json")}); err == nil {
		t.Error("unwritable state file accepted")
	}
	// The state file becomes unwritable while running: New refuses to issue
	// a UID beyond the reserved time.
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	clk := &fakeClock{}
	clk.us.Store(1_000_000_000)
	g, err := NewGenerator(Options{Product: LibreDCM, StateFile: filepath.Join(sub, "s.json"), Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.New(); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(sub); err != nil {
		t.Fatal(err)
	}
	clk.us.Add(2 * reserveAheadMicro)
	if u, err := g.New(); err == nil {
		t.Fatalf("issued %s without being able to record the time mark", u)
	}
}

// Verifies: DCM-SRS-099
func TestGeneratedUIDMaxLength(t *testing.T) {
	clk := &fakeClock{}
	clk.us.Store(99_999_999_999_999_999) // year 5138, 17 digits
	g, err := NewGenerator(Options{Product: LibreKey, Now: clk.Now})
	if err != nil {
		t.Fatal(err)
	}
	g.site = maxRandomSite
	g.prefix = GeneratedArc + ".4." + strconv.FormatUint(maxRandomSite, 10) + "."
	g.last, g.counter = clk.us.Load(), maxCounter-1
	u, err := g.New()
	if err != nil {
		t.Fatal(err)
	}
	if len(u) > MaxLen || Valid(u) != nil {
		t.Fatalf("longest UID %q (%d chars) is not valid", u, len(u))
	}
}
