package fakepeer

import (
	"net/http"
	"sync"
	"testing"
)

// The test server's concurrency measurement is itself a control
// (DVT-SRS-155 rests on it): parallel requests must be counted as
// parallel, and strictly sequential ones as one at a time, however the
// scheduler orders the connection callbacks.
func TestWebConcurrencyMeasurement(t *testing.T) {
	get := func(w *Web) {
		c := &http.Client{Transport: &http.Transport{DisableKeepAlives: true}}
		resp, err := c.Get(w.URL + "/studies")
		if err != nil {
			t.Error(err)
			return
		}
		_ = resp.Body.Close()
	}
	seq, err := StartWeb(nil, WebScript{})
	if err != nil {
		t.Fatal(err)
	}
	defer seq.Close()
	for i := 0; i < 50; i++ {
		get(seq)
	}
	if n := seq.MaxConcurrent(); n != 1 {
		t.Fatalf("50 sequential requests: max %d in progress, want 1", n)
	}
	par, err := StartWeb(nil, WebScript{})
	if err != nil {
		t.Fatal(err)
	}
	defer par.Close()
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); get(par) }()
	}
	wg.Wait()
	if n := par.MaxConcurrent(); n < 2 {
		t.Fatalf("4 parallel requests: max %d in progress, want at least 2", n)
	}
}
