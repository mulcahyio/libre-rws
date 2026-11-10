package licensing

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Ack is an administrator's acknowledgement that the node is unlicensed.
type Ack struct {
	By    string    `json:"by"`
	At    time.Time `json:"at"`
	State string    `json:"license_state"` // license state when acknowledged
}

// ValidAt reports whether the acknowledgement still silences reminders.
func (a Ack) ValidAt(now time.Time) bool {
	return !a.At.IsZero() && !now.Before(a.At) && now.Sub(a.At) < AckValidity
}

func readAck(path string) (Ack, error) {
	var a Ack
	b, err := os.ReadFile(path) // #nosec G304 -- path comes from the licence configuration, not from the network
	if err != nil {
		return a, err
	}
	err = json.Unmarshal(b, &a)
	return a, err
}

// writeAck writes atomically (temp file + rename) so the running node never
// reads a half-written file.
func writeAck(path string, a Ack) error {
	a.By = strings.TrimSpace(a.By)
	if a.By == "" {
		return errors.New("acknowledgement needs the administrator's name")
	}
	b, err := json.MarshalIndent(a, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil { // #nosec G301 -- data directory shared with the licence file; holds no secrets
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil { // #nosec G306 -- administrator name, time and state only; no secrets
		return err
	}
	return os.Rename(tmp, path)
}
