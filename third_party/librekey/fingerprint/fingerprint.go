// Package fingerprint identifies a machine so a license can be locked to it.
//
// A fingerprint has three parts, each a short hash of one hardware or OS
// identifier: the OS machine id, the board UUID and the system disk serial.
// A license still matches when one part changes (a disk swap, an OS
// reinstall), so ordinary repairs don't need a new license.
//
// In a container the OS identifiers belong to the image, not the PC, so the
// installer passes the host's machine id in LIBREKEY_HOST_ID and an install
// id is kept in the data directory.
package fingerprint

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

const prefix = "fp1"

// Parts of a fingerprint, in order.
type Parts struct {
	Machine string // OS machine id (or host id passed into a container)
	Board   string // motherboard / system UUID (or the install id in a container)
	Disk    string // system disk serial
}

// Options for Collect.
type Options struct {
	// DataDir holds the install id when running in a container; empty = not used.
	DataDir string
	// HostID overrides the machine id (containers: the host's id from the installer).
	HostID string
}

// Collect reads the identifiers of this machine and returns its fingerprint.
func Collect(o Options) (string, Parts, error) {
	p := Parts{}
	if o.HostID == "" {
		o.HostID = os.Getenv("LIBREKEY_HOST_ID")
	}
	if o.HostID != "" {
		p.Machine = strings.TrimSpace(o.HostID)
	} else {
		p.Machine = machineID()
	}
	if o.DataDir != "" {
		id, err := installID(o.DataDir)
		if err != nil {
			return "", p, err
		}
		p.Board = id
	} else {
		p.Board = boardID()
	}
	p.Disk = diskSerial()
	n := 0
	for _, v := range []string{p.Machine, p.Board, p.Disk} {
		if v != "" {
			n++
		}
	}
	if n < 2 {
		return "", p, errors.New("fingerprint: fewer than two machine identifiers are readable")
	}
	return Encode(p), p, nil
}

// Encode turns parts into a fingerprint string: fp1-<machine>-<board>-<disk>,
// each 12 hex characters ("000000000000" when unknown).
func Encode(p Parts) string {
	h := func(s string) string {
		s = strings.ToLower(strings.TrimSpace(s))
		if s == "" {
			return "000000000000"
		}
		sum := sha256.Sum256([]byte("librekey:" + s))
		return hex.EncodeToString(sum[:6])
	}
	return strings.Join([]string{prefix, h(p.Machine), h(p.Board), h(p.Disk)}, "-")
}

// Match reports whether fingerprint have (this machine) matches want (the
// license): at most one known part may differ and at least two must match.
func Match(want, have string) bool {
	if want == have {
		return want != ""
	}
	w, ok1 := split(want)
	h, ok2 := split(have)
	if !ok1 || !ok2 {
		return false
	}
	same, diff := 0, 0
	for i := range w {
		switch {
		case w[i] == "000000000000" || h[i] == "000000000000":
		case w[i] == h[i]:
			same++
		default:
			diff++
		}
	}
	return same >= 2 && diff <= 1
}

// Valid reports whether s looks like a fingerprint.
func Valid(s string) bool { _, ok := split(s); return ok }

func split(s string) ([]string, bool) {
	f := strings.Split(s, "-")
	if len(f) != 4 || f[0] != prefix {
		return nil, false
	}
	for _, x := range f[1:] {
		if len(x) != 12 {
			return nil, false
		}
		if _, err := hex.DecodeString(x); err != nil {
			return nil, false
		}
	}
	return f[1:], true
}

func installID(dir string) (string, error) {
	p := filepath.Join(dir, "librekey-install-id")
	if b, err := os.ReadFile(p); err == nil && len(strings.TrimSpace(string(b))) > 0 {
		return strings.TrimSpace(string(b)), nil
	}
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	id := hex.EncodeToString(b)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(p, []byte(id+"\n"), 0o600); err != nil {
		return "", fmt.Errorf("fingerprint: write install id: %w", err)
	}
	return id, nil
}

func readFirst(paths ...string) string {
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil {
			if s := strings.TrimSpace(string(b)); s != "" {
				return s
			}
		}
	}
	return ""
}

func machineID() string {
	switch runtime.GOOS {
	case "linux":
		return readFirst("/etc/machine-id", "/var/lib/dbus/machine-id")
	case "windows":
		return regValue(`HKLM\SOFTWARE\Microsoft\Cryptography`, "MachineGuid")
	case "darwin":
		out, _ := exec.Command("ioreg", "-rd1", "-c", "IOPlatformExpertDevice").Output()
		for _, l := range strings.Split(string(out), "\n") {
			if strings.Contains(l, "IOPlatformUUID") {
				if i := strings.LastIndex(l, "\""); i > 0 {
					if j := strings.LastIndex(l[:i], "\""); j >= 0 {
						return l[j+1 : i]
					}
				}
			}
		}
	}
	return ""
}

func boardID() string {
	switch runtime.GOOS {
	case "linux":
		return readFirst("/sys/class/dmi/id/product_uuid", "/sys/class/dmi/id/board_serial")
	case "windows":
		return psValue("(Get-CimInstance Win32_ComputerSystemProduct).UUID")
	}
	return ""
}

func diskSerial() string {
	switch runtime.GOOS {
	case "linux":
		// serial of the first non-removable, non-virtual block device
		ents, _ := os.ReadDir("/sys/block")
		for _, e := range ents {
			n := e.Name()
			if strings.HasPrefix(n, "loop") || strings.HasPrefix(n, "ram") || strings.HasPrefix(n, "zram") || strings.HasPrefix(n, "dm-") || strings.HasPrefix(n, "sr") {
				continue
			}
			if readFirst(filepath.Join("/sys/block", n, "removable")) == "1" {
				continue
			}
			if s := readFirst(filepath.Join("/sys/block", n, "device/serial"), filepath.Join("/sys/block", n, "serial"), filepath.Join("/sys/block", n, "device/wwid")); s != "" {
				return s
			}
		}
	case "windows":
		return psValue("(Get-CimInstance Win32_DiskDrive | Where-Object { $_.Index -eq 0 }).SerialNumber")
	}
	return ""
}

func regValue(key, name string) string {
	out, err := exec.Command("reg", "query", key, "/v", name).Output()
	if err != nil {
		return ""
	}
	for _, l := range strings.Split(string(out), "\n") {
		f := strings.Fields(l)
		if len(f) >= 3 && strings.EqualFold(f[0], name) {
			return f[len(f)-1]
		}
	}
	return ""
}

func psValue(expr string) string {
	out, err := exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", expr).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}
