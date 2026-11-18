//go:build engines

// LibreDVT's DICOM identification with the real pynetdicom worker: the
// A-ASSOCIATE-RQ it sends and the Part 10 files its built-in Storage SCP
// writes carry LibreDVT's Implementation Class UID and Version Name, not
// pynetdicom's (DVT-SRS-082). Runs in the LibreDVT test image only.

package engine

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readAssociateRQ reads one A-ASSOCIATE-RQ (PS3.8 §9.3.2) and returns its
// Implementation Class UID (sub-item 52H) and Version Name (55H). It is a
// minimal parser of its own: LibreDVT tests must not use LibreDCM's stack.
func readAssociateRQ(r io.Reader) (uid, name string, err error) {
	var hdr [6]byte
	if _, err = io.ReadFull(r, hdr[:]); err != nil {
		return "", "", err
	}
	if hdr[0] != 0x01 {
		return "", "", fmt.Errorf("PDU type %#x is not A-ASSOCIATE-RQ", hdr[0])
	}
	body := make([]byte, binary.BigEndian.Uint32(hdr[2:]))
	if _, err = io.ReadFull(r, body); err != nil || len(body) < 68 {
		return "", "", fmt.Errorf("short A-ASSOCIATE-RQ: %v", err)
	}
	items := func(b []byte, f func(typ byte, data []byte)) {
		for len(b) >= 4 {
			n := int(binary.BigEndian.Uint16(b[2:]))
			if 4+n > len(b) {
				return
			}
			f(b[0], b[4:4+n])
			b = b[4+n:]
		}
	}
	items(body[68:], func(typ byte, data []byte) {
		if typ != 0x50 { // User Information
			return
		}
		items(data, func(sub byte, v []byte) {
			switch sub {
			case 0x52:
				uid = strings.TrimRight(string(v), "\x00")
			case 0x55:
				name = string(v)
			}
		})
	})
	return uid, name, nil
}

// metaValue returns a File Meta element's value from a Part 10 file
// (Explicit VR Little Endian group 0002).
func metaValue(t *testing.T, file []byte, elem uint16) string {
	t.Helper()
	if len(file) < 132 || string(file[128:132]) != "DICM" {
		t.Fatal("not a Part 10 file")
	}
	for p := 132; p+8 <= len(file) && binary.LittleEndian.Uint16(file[p:]) == 0x0002; {
		e, vr := binary.LittleEndian.Uint16(file[p+2:]), string(file[p+4:p+6])
		n, h := int(binary.LittleEndian.Uint16(file[p+6:])), 8
		if vr == "OB" || vr == "OW" || vr == "UN" || vr == "SQ" || vr == "UT" {
			n, h = int(binary.LittleEndian.Uint32(file[p+8:])), 12
		}
		if p+h+n > len(file) {
			t.Fatal("meta element overruns the file")
		}
		if e == elem {
			return strings.TrimRight(string(file[p+h:p+h+n]), "\x00 ")
		}
		p += h + n
	}
	return ""
}

func freeTCPPort(t *testing.T) int {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = l.Close() }()
	return l.Addr().(*net.TCPAddr).Port
}

// Verifies: DVT-SRS-082
func TestWorkerSendsLibreDVTIdentification(t *testing.T) {
	c := RealConfig(t)
	c.Version = "0.2.0"
	wantUID, wantName := "1.3.6.1.4.1.67686.1.3.1.0.2", "LIBREDVT_0_2"

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()
	type got struct {
		uid, name string
		err       error
	}
	ch := make(chan got, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			ch <- got{err: err}
			return
		}
		defer func() { _ = conn.Close() }()
		_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
		u, n, err := readAssociateRQ(conn)
		ch <- got{u, n, err}
		_, _ = conn.Write([]byte{0x03, 0, 0, 0, 0, 4, 0, 1, 1, 1}) // A-ASSOCIATE-RJ
	}()
	r, raw, err := c.Echo(context.Background(), Peer{Host: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port,
		CalledAET: "PROBE", CallingAET: "LIBREDVT", Timeout: 5 * time.Second})
	if err != nil {
		t.Fatalf("echo: %v\n%s", err, raw)
	}
	if !r.Rejected {
		t.Fatalf("worker did not see the rejection: %+v", r)
	}
	g := <-ch
	if g.err != nil || g.uid != wantUID || g.name != wantName {
		t.Fatalf("A-ASSOCIATE-RQ carried %q / %q (%v), want %q / %q", g.uid, g.name, g.err, wantUID, wantName)
	}

	// The built-in Storage SCP writes received files as LibreDVT.
	scpPort := freeTCPPort(t)
	fakePort := freeTCPPort(t)
	cmd := exec.Command("python3", "../../worker/testdata/fake_scp.py", "--port", fmt.Sprint(fakePort),
		"--ae", "FAKEPACS", "--mode", "ok", "--dest", fmt.Sprintf("LIBREDVT=127.0.0.1:%d", scpPort))
	out, _ := cmd.StdoutPipe()
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if s := bufio.NewScanner(out); !s.Scan() || s.Text() != "ready" {
		t.Fatal("fake SCP did not start")
	}
	peer := Peer{Host: "127.0.0.1", Port: fakePort, CalledAET: "FAKEPACS", CallingAET: "LIBREDVT", Timeout: 10 * time.Second}
	fixture := filepath.Join(os.Getenv("LIBRE_DVT_TEST_ASSETS"), "plans", "fixtures", "ct_explicit_le.dcm")
	sr, raw, err := c.Store(context.Background(), peer, []string{fixture})
	if err != nil || len(sr.Files) != 1 || sr.Files[0].Status == nil || *sr.Files[0].Status != 0 {
		t.Fatalf("store: %v %+v\n%s", err, sr, raw)
	}
	dir := t.TempDir()
	mr, raw, err := c.Move(context.Background(), peer, SCP{AETitle: "LIBREDVT", Port: scpPort}, dir,
		[]string{sr.Files[0].StudyInstanceUID}, 1)
	if err != nil || len(mr.Received) != 1 {
		t.Fatalf("move: %v %+v\n%s", err, mr, raw)
	}
	file, err := os.ReadFile(mr.Received[0].File)
	if err != nil {
		t.Fatal(err)
	}
	if u, n := metaValue(t, file, 0x0012), metaValue(t, file, 0x0013); u != wantUID || n != wantName {
		t.Fatalf("received file meta (0002,0012)/(0002,0013) = %q / %q, want %q / %q", u, n, wantUID, wantName)
	}
}
