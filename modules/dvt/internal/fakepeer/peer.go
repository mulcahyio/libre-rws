package fakepeer

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

// Test identification of the peer, under the LibreDVT test-data arc
// (docs/qms/uid-registry.md §6).
const (
	ImplUID     = "1.3.6.1.4.1.67686.9.3.1"
	ImplVersion = "FAKEPEER_1"
	AppContext  = "1.2.840.10008.3.1.1.1"
	CTImage     = "1.2.840.10008.5.1.4.1.1.2"
	ExplicitLE  = "1.2.840.10008.1.2.1"
	ImplicitLE  = "1.2.840.10008.1.2"
)

// PDU types (PS3.8 §9.3).
const (
	PDUAssociateRQ = 1
	PDUAssociateAC = 2
	PDUAssociateRJ = 3
	PDUData        = 4
	PDUReleaseRQ   = 5
	PDUReleaseRP   = 6
	PDUAbort       = 7
)

// RQContext is a proposed presentation context.
type RQContext struct {
	ID               byte
	AbstractSyntax   string
	TransferSyntaxes []string
}

// RQ is what the peer read from an A-ASSOCIATE-RQ.
type RQ struct {
	Called, Calling      string
	Contexts             []RQContext
	MaxPDU               uint32
	ImplUID, ImplVersion string
	Async                *[2]uint16
	Roles                map[string][2]byte
}

// ContextReply is the peer's answer for one presentation context.
type ContextReply struct {
	Result         byte // 0 acceptance, 1 user rejection, 2 no reason, 3 abstract syntax, 4 transfer syntaxes
	TransferSyntax string
}

// MoveScript makes the peer act as a C-MOVE SCP that opens the storage
// sub-association itself (only the association, no C-STORE), so a test can
// see whether LibreDVT's Storage SCP accepts it (DVT-SRS-108).
type MoveScript struct {
	Addr       string      // LibreDVT's Storage SCP
	TLS        *tls.Config // nil: clear text
	CallingAET string
}

// Script chooses how the peer behaves.
type Script struct {
	Reject  *[3]byte // answer A-ASSOCIATE-RJ result, source, reason
	Abort   *[2]byte // answer A-ABORT source, reason
	Close   bool     // close without answering
	Garbage bool     // answer bytes that are not a PDU
	Silent  bool     // never answer

	Contexts      func(RQContext) ContextReply // nil: accept with the first proposed syntax
	OmitIDs       []byte                       // leave these contexts out of the A-ASSOCIATE-AC
	ExtraID       byte                         // add an accepted context that was not proposed
	MaxPDU        uint32                       // 0 = 16384
	ImplUID       string                       // "" = ImplUID
	ImplVersion   string                       // "" = ImplVersion
	NoImplVersion bool
	Async         *[2]uint16         // asynchronous operations window reply
	Roles         map[string][2]byte // role selection reply by abstract syntax

	Release string // "" reply, "abort", "close", "silent"
	Move    *MoveScript
}

// SubResult records a move sub-association.
type SubResult struct {
	Error    string // dial, handshake or read error
	Answer   byte   // PDU type received in answer to the A-ASSOCIATE-RQ
	Released bool
}

// Conn records one connection the peer saw.
type Conn struct {
	Handshake  string // TLS handshake error, "" when it succeeded or no TLS
	TLSVersion uint16
	ClientCert bool
	ReadError  string
	RQ         *RQ
	PDUs       []byte // PDU types received after the A-ASSOCIATE-RQ
	PData      int
	Sub        *SubResult
}

// Peer is a scripted DICOM upper-layer peer.
type Peer struct {
	Port   int
	ln     net.Listener
	tlsCfg *tls.Config
	script Script

	mu      sync.Mutex
	conns   []*Conn
	cur     int
	maxConc int
	wg      sync.WaitGroup
}

// Start listens on 127.0.0.1 with an ephemeral port. tlsCfg nil = clear text.
func Start(tlsCfg *tls.Config, s Script) (*Peer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	p := &Peer{Port: ln.Addr().(*net.TCPAddr).Port, ln: ln, tlsCfg: tlsCfg, script: s}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			p.wg.Add(1)
			go func() {
				defer p.wg.Done()
				p.serve(c)
			}()
		}
	}()
	return p, nil
}

// Close stops the peer and waits for its connections.
func (p *Peer) Close() {
	_ = p.ln.Close()
	p.wg.Wait()
}

// Conns returns copies of the recorded connections.
func (p *Peer) Conns() []Conn {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Conn, len(p.conns))
	for i, c := range p.conns {
		out[i] = *c
	}
	return out
}

// MaxConcurrent is the largest number of connections open at once.
func (p *Peer) MaxConcurrent() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.maxConc
}

func (p *Peer) update(f func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	f()
}

func (p *Peer) serve(c net.Conn) {
	defer c.Close()
	rec := &Conn{}
	p.update(func() {
		p.conns = append(p.conns, rec)
		p.cur++
		if p.cur > p.maxConc {
			p.maxConc = p.cur
		}
	})
	defer p.update(func() { p.cur-- })
	if p.tlsCfg != nil {
		tc := tls.Server(c, p.tlsCfg)
		_ = tc.SetDeadline(time.Now().Add(10 * time.Second))
		if err := tc.Handshake(); err != nil {
			p.update(func() { rec.Handshake = err.Error() })
			return
		}
		st := tc.ConnectionState()
		p.update(func() { rec.TLSVersion, rec.ClientCert = st.Version, len(st.PeerCertificates) > 0 })
		c = tc
	}
	_ = c.SetDeadline(time.Now().Add(30 * time.Second))
	typ, body, err := ReadPDU(c)
	if err != nil {
		p.update(func() { rec.ReadError = err.Error() })
		return
	}
	if typ != PDUAssociateRQ {
		p.update(func() { rec.PDUs = append(rec.PDUs, typ) })
		return
	}
	rq, err := ParseRQ(body)
	if err != nil {
		p.update(func() { rec.ReadError = err.Error() })
		return
	}
	p.update(func() { rec.RQ = rq })
	s := p.script
	switch {
	case s.Silent:
		_, _ = io.Copy(io.Discard, c)
		return
	case s.Close:
		return
	case s.Garbage:
		_, _ = c.Write([]byte("GARBAGE, NOT A DICOM PDU\r\n"))
		return
	case s.Abort != nil:
		_, _ = c.Write(Abort(s.Abort[0], s.Abort[1]))
		return
	case s.Reject != nil:
		_, _ = c.Write(pdu(PDUAssociateRJ, []byte{0, s.Reject[0], s.Reject[1], s.Reject[2]}))
		return
	}
	if _, err := c.Write(BuildAC(rq, s)); err != nil {
		return
	}
	st := &connState{bufs: map[byte]*bytes.Buffer{}, pending: map[byte]cmdSet{}}
	for {
		typ, body, err := ReadPDU(c)
		if err != nil {
			return
		}
		p.update(func() { rec.PDUs = append(rec.PDUs, typ) })
		switch typ {
		case PDUData:
			p.update(func() { rec.PData++ })
			if !p.pdata(c, body, st, rec) {
				return
			}
		case PDUReleaseRQ:
			switch s.Release {
			case "abort":
				_, _ = c.Write(Abort(2, 0))
			case "close":
			case "silent":
				_, _ = io.Copy(io.Discard, c)
			default:
				_, _ = c.Write(pdu(PDUReleaseRP, make([]byte, 4)))
			}
			return
		default:
			return
		}
	}
}

// --- PDUs ---------------------------------------------------------------

func pdu(t byte, body []byte) []byte {
	h := []byte{t, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint32(h[2:], uint32(len(body))) // #nosec G115 -- test PDUs are small
	return append(h, body...)
}

func item(t byte, v []byte) []byte {
	h := []byte{t, 0, 0, 0}
	binary.BigEndian.PutUint16(h[2:], uint16(len(v))) // #nosec G115 -- items are small
	return append(h, v...)
}

func aeField(s string) []byte {
	b := []byte(s + strings.Repeat(" ", 16))
	return b[:16]
}

// Abort encodes an A-ABORT PDU.
func Abort(source, reason byte) []byte { return pdu(PDUAbort, []byte{0, 0, source, reason}) }

// ReadPDU reads one PDU (at most 1 MiB).
func ReadPDU(r io.Reader) (byte, []byte, error) {
	h := make([]byte, 6)
	if _, err := io.ReadFull(r, h); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(h[2:])
	if n > 1<<20 {
		return 0, nil, fmt.Errorf("PDU length %d too large", n)
	}
	b := make([]byte, n)
	if _, err := io.ReadFull(r, b); err != nil {
		return 0, nil, err
	}
	return h[0], b, nil
}

type rawItem struct {
	t byte
	v []byte
}

func items(b []byte) ([]rawItem, error) {
	var out []rawItem
	for len(b) > 0 {
		if len(b) < 4 {
			return nil, errors.New("truncated item")
		}
		n := int(binary.BigEndian.Uint16(b[2:]))
		if 4+n > len(b) {
			return nil, errors.New("item runs past its container")
		}
		out = append(out, rawItem{b[0], b[4 : 4+n]})
		b = b[4+n:]
	}
	return out, nil
}

func uidText(b []byte) string { return strings.TrimRight(string(b), "\x00 ") }

// ParseRQ reads an A-ASSOCIATE-RQ body.
func ParseRQ(b []byte) (*RQ, error) {
	if len(b) < 68 {
		return nil, errors.New("A-ASSOCIATE-RQ too short")
	}
	rq := &RQ{Called: strings.TrimSpace(string(b[4:20])), Calling: strings.TrimSpace(string(b[20:36])), Roles: map[string][2]byte{}}
	its, err := items(b[68:])
	if err != nil {
		return nil, err
	}
	for _, it := range its {
		switch it.t {
		case 0x20:
			if len(it.v) < 4 {
				return nil, errors.New("presentation context item too short")
			}
			c := RQContext{ID: it.v[0]}
			subs, err := items(it.v[4:])
			if err != nil {
				return nil, err
			}
			for _, s := range subs {
				switch s.t {
				case 0x30:
					c.AbstractSyntax = uidText(s.v)
				case 0x40:
					c.TransferSyntaxes = append(c.TransferSyntaxes, uidText(s.v))
				}
			}
			rq.Contexts = append(rq.Contexts, c)
		case 0x50:
			subs, err := items(it.v)
			if err != nil {
				return nil, err
			}
			for _, s := range subs {
				switch s.t {
				case 0x51:
					if len(s.v) == 4 {
						rq.MaxPDU = binary.BigEndian.Uint32(s.v)
					}
				case 0x52:
					rq.ImplUID = uidText(s.v)
				case 0x53:
					if len(s.v) == 4 {
						rq.Async = &[2]uint16{binary.BigEndian.Uint16(s.v), binary.BigEndian.Uint16(s.v[2:])}
					}
				case 0x54:
					if len(s.v) >= 4 {
						n := int(binary.BigEndian.Uint16(s.v))
						if 2+n+2 == len(s.v) {
							rq.Roles[uidText(s.v[2:2+n])] = [2]byte{s.v[2+n], s.v[3+n]}
						}
					}
				case 0x55:
					rq.ImplVersion = strings.TrimSpace(string(s.v))
				}
			}
		}
	}
	return rq, nil
}

// BuildRQ encodes an A-ASSOCIATE-RQ (used for the move sub-association).
func BuildRQ(called, calling string, contexts []RQContext) []byte {
	var v []byte
	v = append(v, item(0x10, []byte(AppContext))...)
	for _, c := range contexts {
		sub := item(0x30, []byte(c.AbstractSyntax))
		for _, ts := range c.TransferSyntaxes {
			sub = append(sub, item(0x40, []byte(ts))...)
		}
		v = append(v, item(0x20, append([]byte{c.ID, 0, 0, 0}, sub...))...)
	}
	ui := item(0x51, []byte{0, 0, 0x40, 0})
	ui = append(ui, item(0x52, []byte(ImplUID))...)
	ui = append(ui, item(0x55, []byte(ImplVersion))...)
	v = append(v, item(0x50, ui)...)
	hdr := append([]byte{0, 1, 0, 0}, aeField(called)...)
	hdr = append(hdr, aeField(calling)...)
	hdr = append(hdr, make([]byte, 32)...)
	return pdu(PDUAssociateRQ, append(hdr, v...))
}

// BuildAC encodes the peer's A-ASSOCIATE-AC for rq under script s.
func BuildAC(rq *RQ, s Script) []byte {
	var v []byte
	v = append(v, item(0x10, []byte(AppContext))...)
	omit := map[byte]bool{}
	for _, id := range s.OmitIDs {
		omit[id] = true
	}
	for _, c := range rq.Contexts {
		if omit[c.ID] {
			continue
		}
		r := ContextReply{TransferSyntax: first(c.TransferSyntaxes)}
		if s.Contexts != nil {
			r = s.Contexts(c)
		}
		v = append(v, item(0x21, append([]byte{c.ID, 0, r.Result, 0}, item(0x40, []byte(r.TransferSyntax))...))...)
	}
	if s.ExtraID != 0 {
		v = append(v, item(0x21, append([]byte{s.ExtraID, 0, 0, 0}, item(0x40, []byte(ImplicitLE))...))...)
	}
	maxPDU := s.MaxPDU
	if maxPDU == 0 {
		maxPDU = 16384
	}
	mp := make([]byte, 4)
	binary.BigEndian.PutUint32(mp, maxPDU)
	ui := item(0x51, mp)
	impl := s.ImplUID
	if impl == "" {
		impl = ImplUID
	}
	ui = append(ui, item(0x52, []byte(impl))...)
	if s.Async != nil {
		a := make([]byte, 4)
		binary.BigEndian.PutUint16(a, s.Async[0])
		binary.BigEndian.PutUint16(a[2:], s.Async[1])
		ui = append(ui, item(0x53, a)...)
	}
	for uid, r := range s.Roles {
		b := make([]byte, 2)
		binary.BigEndian.PutUint16(b, uint16(len(uid))) // #nosec G115 -- UIDs are at most 64 bytes
		b = append(append(b, []byte(uid)...), r[0], r[1])
		ui = append(ui, item(0x54, b)...)
	}
	if !s.NoImplVersion {
		ver := s.ImplVersion
		if ver == "" {
			ver = ImplVersion
		}
		ui = append(ui, item(0x55, []byte(ver))...)
	}
	v = append(v, item(0x50, ui)...)
	hdr := append([]byte{0, 1, 0, 0}, aeField(rq.Called)...)
	hdr = append(hdr, aeField(rq.Calling)...)
	hdr = append(hdr, make([]byte, 32)...)
	return pdu(PDUAssociateAC, append(hdr, v...))
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

// --- DIMSE commands (implicit VR little endian, PS3.7 §9.3, §E) -----------

type cmdSet map[uint32][]byte

// connState holds one connection's partly received commands.
type connState struct {
	bufs    map[byte]*bytes.Buffer
	pending map[byte]cmdSet // command waiting for its data set
}

func parseCmd(b []byte) (cmdSet, error) {
	cs := cmdSet{}
	for len(b) > 0 {
		if len(b) < 8 {
			return nil, errors.New("truncated command element")
		}
		tag := uint32(binary.LittleEndian.Uint16(b))<<16 | uint32(binary.LittleEndian.Uint16(b[2:]))
		n := int(binary.LittleEndian.Uint32(b[4:]))
		if 8+n > len(b) || n < 0 {
			return nil, errors.New("command element runs past the command set")
		}
		cs[tag] = b[8 : 8+n]
		b = b[8+n:]
	}
	return cs, nil
}

func (c cmdSet) us(tag uint32) uint16 {
	if v := c[tag]; len(v) == 2 {
		return binary.LittleEndian.Uint16(v)
	}
	return 0
}

func elem(tag uint32, v []byte) []byte {
	b := make([]byte, 8, 8+len(v))
	binary.LittleEndian.PutUint16(b, uint16(tag>>16))
	binary.LittleEndian.PutUint16(b[2:], uint16(tag))    // #nosec G115 -- element number: the low 16 bits by definition
	binary.LittleEndian.PutUint32(b[4:], uint32(len(v))) // #nosec G115 -- command values are small
	return append(b, v...)
}

func us(v uint16) []byte { b := make([]byte, 2); binary.LittleEndian.PutUint16(b, v); return b }

func uidVal(s string) []byte {
	b := []byte(s)
	if len(b)%2 == 1 {
		b = append(b, 0)
	}
	return b
}

func encodeCmd(elems ...[]byte) []byte {
	body := bytes.Join(elems, nil)
	gl := make([]byte, 4)
	binary.LittleEndian.PutUint32(gl, uint32(len(body))) // #nosec G115 -- command sets are small
	return append(elem(0x00000000, gl), body...)
}

func writeCommand(w io.Writer, pcid byte, cmd []byte) error {
	pdv := make([]byte, 4)
	binary.BigEndian.PutUint32(pdv, uint32(len(cmd)+2)) // #nosec G115 -- command sets are small
	pdv = append(pdv, pcid, 0x03)
	_, err := w.Write(pdu(PDUData, append(pdv, cmd...)))
	return err
}

// pdata handles one P-DATA-TF: C-ECHO and C-MOVE requests get answers.
// It returns false when the connection should end.
func (p *Peer) pdata(c net.Conn, body []byte, st *connState, rec *Conn) bool {
	cmds := st.bufs
	for len(body) >= 6 {
		n := int(binary.BigEndian.Uint32(body))
		if n < 2 || 4+n > len(body) {
			return false
		}
		pcid, ctrl, data := body[4], body[5], body[6:4+n]
		body = body[4+n:]
		if ctrl&0x01 == 0 { // data set fragment (C-MOVE identifier): answer once it is complete
			if ctrl&0x02 != 0 && st.pending[pcid] != nil {
				cs := st.pending[pcid]
				delete(st.pending, pcid)
				if !p.answer(c, pcid, cs, rec) {
					return false
				}
			}
			continue
		}
		buf := cmds[pcid]
		if buf == nil {
			buf = &bytes.Buffer{}
			cmds[pcid] = buf
		}
		buf.Write(data)
		if ctrl&0x02 == 0 {
			continue
		}
		cs, err := parseCmd(buf.Bytes())
		buf.Reset()
		if err != nil {
			return false
		}
		if cs.us(0x00000800) != 0x0101 { // a data set follows
			st.pending[pcid] = cs
			continue
		}
		if !p.answer(c, pcid, cs, rec) {
			return false
		}
	}
	return true
}

// answer responds to a complete request: C-ECHO and C-MOVE get success.
func (p *Peer) answer(c net.Conn, pcid byte, cs cmdSet, rec *Conn) bool {
	sop := uidText(cs[0x00000002])
	msgID := cs.us(0x00000110)
	switch cs.us(0x00000100) {
	case 0x0030: // C-ECHO-RQ
		rsp := encodeCmd(elem(0x00000002, uidVal(sop)), elem(0x00000100, us(0x8030)), elem(0x00000120, us(msgID)),
			elem(0x00000800, us(0x0101)), elem(0x00000900, us(0)))
		return writeCommand(c, pcid, rsp) == nil
	case 0x0021: // C-MOVE-RQ
		sub := p.subAssociation(strings.TrimSpace(uidText(cs[0x00000600])))
		p.update(func() { rec.Sub = sub })
		rsp := encodeCmd(elem(0x00000002, uidVal(sop)), elem(0x00000100, us(0x8021)), elem(0x00000120, us(msgID)),
			elem(0x00000800, us(0x0101)), elem(0x00000900, us(0)),
			elem(0x00001021, us(0)), elem(0x00001022, us(0)), elem(0x00001023, us(0)))
		return writeCommand(c, pcid, rsp) == nil
	}
	return true
}

func (p *Peer) subAssociation(dest string) *SubResult {
	m := p.script.Move
	r := &SubResult{}
	if m == nil {
		r.Error = "no move script"
		return r
	}
	conn, err := net.DialTimeout("tcp", m.Addr, 5*time.Second)
	if err != nil {
		r.Error = err.Error()
		return r
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	var c net.Conn = conn
	if m.TLS != nil {
		tc := tls.Client(conn, m.TLS)
		if err := tc.Handshake(); err != nil {
			r.Error = "handshake: " + err.Error()
			return r
		}
		c = tc
	}
	if _, err := c.Write(BuildRQ(dest, m.CallingAET, []RQContext{{ID: 1, AbstractSyntax: CTImage, TransferSyntaxes: []string{ExplicitLE, ImplicitLE}}})); err != nil {
		r.Error = "write: " + err.Error()
		return r
	}
	typ, _, err := ReadPDU(c)
	if err != nil {
		r.Error = "read: " + err.Error()
		return r
	}
	r.Answer = typ
	if typ == PDUAssociateAC {
		if _, err := c.Write(pdu(PDUReleaseRQ, make([]byte, 4))); err == nil {
			if t, _, err := ReadPDU(c); err == nil && t == PDUReleaseRP {
				r.Released = true
			}
		}
	}
	return r
}
