//go:build engines

// Association and TLS positive controls with the real Python worker against
// LibreDVT's Go test peer (internal/fakepeer: net and crypto/tls, none of the
// worker's libraries). Runs in the LibreDVT test image only.

package runner

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/engine"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/fakepeer"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
	"github.com/mulcahyio/libre-rws/modules/dvt/internal/store"
)

func startPeer(t *testing.T, cfg *tls.Config, s fakepeer.Script) *fakepeer.Peer {
	t.Helper()
	p, err := fakepeer.Start(cfg, s)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(p.Close)
	return p
}

func peerTarget(p *fakepeer.Peer, tl *store.TLS) *store.Target {
	tg := &store.Target{Name: "t", Class: store.Lab, Host: "127.0.0.1", Port: p.Port, CalledAET: "FAKEPEER", CallingAET: "LIBREDVT", TLS: tl}
	if err := tg.Normalize(); err != nil {
		panic(err)
	}
	return tg
}

func runReal(t *testing.T, src string, tg *store.Target, scp engine.SCP) *result.Run {
	t.Helper()
	e := realEngines(t)
	e.Version = "0.2.0"
	rn := &Runner{Engines: e, SCP: scp, WorkDir: t.TempDir(), Version: "0.2.0"}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	return rn.Run(ctx, "id", setup(t, src), tg, nil)
}

func msgs(r *result.Run) string { return strings.Join(r.Steps[0].Messages, " | ") }

// goodScript meets every expectation of assocPlan.
func goodScript() fakepeer.Script {
	return fakepeer.Script{
		ImplUID: "1.2.3.9", ImplVersion: "PEER_1", MaxPDU: 16384,
		Async: &[2]uint16{1, 1},
		Roles: map[string][2]byte{ctImage: {1, 1}},
		Contexts: func(c fakepeer.RQContext) fakepeer.ContextReply {
			if c.AbstractSyntax == "1.2.3.4.5" {
				return fakepeer.ContextReply{Result: 3, TransferSyntax: c.TransferSyntaxes[0]}
			}
			return fakepeer.ContextReply{TransferSyntax: c.TransferSyntaxes[0]}
		},
	}
}

// Verifies: DVT-SRS-090, DVT-SRS-091, DVT-SRS-093, DVT-SRS-094, DVT-SRS-155
func TestRealAssociateNegotiation(t *testing.T) {
	p := startPeer(t, nil, goodScript())
	r := runReal(t, assocPlan, peerTarget(p, nil), engine.SCP{})
	if r.Verdict != result.Pass {
		t.Fatalf("matching peer: %s %s", r.Verdict, msgs(r))
	}
	// What the worker proposed, as the independent peer read it.
	c := p.Conns()
	if len(c) != 1 || c[0].RQ == nil {
		t.Fatalf("peer saw %+v", c)
	}
	rq := c[0].RQ
	uid, name := engine.Implementation("0.2.0")
	if rq.Called != "FAKEPEER" || rq.Calling != "LIBREDVT" || rq.ImplUID != uid || rq.ImplVersion != name || rq.MaxPDU != 16384 ||
		len(rq.Contexts) != 3 || rq.Contexts[0].ID != 1 || rq.Contexts[2].ID != 5 || rq.Contexts[1].AbstractSyntax != ctImage ||
		strings.Join(rq.Contexts[1].TransferSyntaxes, ",") != explicitLE+","+implicitLE ||
		rq.Roles[ctImage] != [2]byte{1, 1} || rq.Async == nil || *rq.Async != [2]uint16{2, 1} {
		t.Fatalf("A-ASSOCIATE-RQ as seen by the peer: %+v", rq)
	}
	// The peer saw a release and nothing else after the association.
	if string(c[0].PDUs) != string([]byte{fakepeer.PDUReleaseRQ}) || p.MaxConcurrent() != 1 {
		t.Fatalf("after association: PDUs %v, max concurrent %d", c[0].PDUs, p.MaxConcurrent())
	}
	a := findExchange(r.Steps[0].Evidence.Exchanges, engine.OpAssociate).Association
	if a.ImplClassUID != "1.2.3.9" || a.ImplVersion != "PEER_1" || a.MaxPDU != 16384 || a.AsyncOps == nil || a.AsyncOps.Invoked != 1 || !a.Released {
		t.Fatalf("negotiated values: %+v", a)
	}
	type tc struct {
		mod  func(*fakepeer.Script)
		want string
	}
	cases := map[string]tc{
		"rejects an expected context": {func(s *fakepeer.Script) {
			inner := s.Contexts
			s.Contexts = func(c fakepeer.RQContext) fakepeer.ContextReply {
				if c.AbstractSyntax == ctImage {
					return fakepeer.ContextReply{Result: 3, TransferSyntax: explicitLE}
				}
				return inner(c)
			}
		}, "fail:assoc.context.result,fail:assoc.context.transfer_syntax"},
		"accepts a context expected rejected": {func(s *fakepeer.Script) { s.Contexts = nil; s.Roles = map[string][2]byte{ctImage: {1, 1}} },
			"fail:assoc.context.result"},
		"other transfer syntax": {func(s *fakepeer.Script) {
			inner := s.Contexts
			s.Contexts = func(c fakepeer.RQContext) fakepeer.ContextReply {
				if c.AbstractSyntax == ctImage {
					return fakepeer.ContextReply{TransferSyntax: implicitLE}
				}
				return inner(c)
			}
		}, "fail:assoc.context.transfer_syntax"},
		"context left out":           {func(s *fakepeer.Script) { s.OmitIDs = []byte{1} }, "fail:assoc.context.result,problem:protocol"},
		"context not proposed":       {func(s *fakepeer.Script) { s.ExtraID = 7 }, "problem:protocol"},
		"max PDU differs":            {func(s *fakepeer.Script) { s.MaxPDU = 4096 }, "fail:assoc.max_pdu"},
		"Implementation Class UID":   {func(s *fakepeer.Script) { s.ImplUID = "1.2.3.10" }, "fail:assoc.impl_class_uid"},
		"Implementation Version":     {func(s *fakepeer.Script) { s.ImplVersion = "OTHER_1" }, "fail:assoc.impl_version"},
		"no Implementation Version":  {func(s *fakepeer.Script) { s.NoImplVersion = true }, "fail:assoc.impl_version"},
		"SCP role refused":           {func(s *fakepeer.Script) { s.Roles = map[string][2]byte{ctImage: {1, 0}} }, "fail:assoc.context.roles"},
		"no role selection reply":    {func(s *fakepeer.Script) { s.Roles = nil }, "fail:assoc.context.roles"},
		"aborts on release (passes)": {func(s *fakepeer.Script) { s.Release = "abort" }, "warn:assoc.release"},
		"silent on release (passes)": {func(s *fakepeer.Script) { s.Release = "silent" }, "warn:assoc.release"},
	}
	for name, c := range cases {
		s := goodScript()
		c.mod(&s)
		p := startPeer(t, nil, s)
		src := strings.Replace(assocPlan, "name: assoc", "name: assoc\ntimeout: 2s", 1)
		r := runReal(t, src, peerTarget(p, nil), engine.SCP{})
		want := result.Fail
		if strings.HasPrefix(c.want, "warn:") {
			want = result.PassWithWarnings
		}
		if r.Verdict != want || failedIDs(r.Steps[0].Evidence) != c.want {
			t.Errorf("%s: %s, findings %q (want %q): %s", name, r.Verdict, failedIDs(r.Steps[0].Evidence), c.want, msgs(r))
		}
	}
	// Abort on release with a failed check fails.
	s := goodScript()
	s.Release, s.MaxPDU = "abort", 1024
	p = startPeer(t, nil, s)
	if r := runReal(t, assocPlan, peerTarget(p, nil), engine.SCP{}); r.Verdict != result.Fail {
		t.Fatalf("abort on release + failed check: %s", r.Verdict)
	}
}

// Verifies: DVT-SRS-092, DVT-SRS-095, DVT-SRS-150
func TestRealAssociateRejectAbortCloseGarbageSilence(t *testing.T) {
	rj := func(a, b, c byte) fakepeer.Script { return fakepeer.Script{Reject: &[3]byte{a, b, c}} }
	for name, c := range map[string]struct {
		s    fakepeer.Script
		want result.Verdict
	}{
		"matching rejection": {rj(1, 1, 7), result.Pass},
		"other reason":       {rj(1, 1, 3), result.Fail},
		"other source":       {rj(1, 3, 1), result.Fail},
		"accepted":           {fakepeer.Script{}, result.Fail},
	} {
		p := startPeer(t, nil, c.s)
		if r := runReal(t, rejectPlan, peerTarget(p, nil), engine.SCP{}); r.Verdict != c.want {
			t.Errorf("%s: %s %s", name, r.Verdict, msgs(r))
		}
	}
	events := map[string]struct {
		s          fakepeer.Script
		kind, text string
	}{
		"abort":   {fakepeer.Script{Abort: &[2]byte{2, 0}}, result.ProblemProtocol, "A-ABORT source 2, reason 0"},
		"close":   {fakepeer.Script{Close: true}, result.ProblemConnectionClosed, "closed the connection"},
		"garbage": {fakepeer.Script{Garbage: true}, result.ProblemProtocol, "malformed PDU"},
		"silence": {fakepeer.Script{Silent: true}, result.ProblemTimeout, "no answer within the timeout"},
	}
	src := strings.Replace(assocPlan, "name: assoc", "name: assoc\ntimeout: 1s", 1)
	for name, e := range events {
		p := startPeer(t, nil, e.s)
		r := runReal(t, src, peerTarget(p, nil), engine.SCP{})
		ev := r.Steps[0].Evidence
		if r.Verdict != result.Fail || len(ev.Errors) == 0 || ev.Errors[0].Kind != e.kind || !strings.Contains(msgs(r), e.text) {
			t.Errorf("%s: %s %+v %s", name, r.Verdict, ev.Errors, msgs(r))
		}
	}
}

// pki builds the synthetic PKI and the peer certificates the TLS tests use.
type testPKI struct {
	*fakepeer.PKI
	good, dnsOnly, ipOnly, wrongHost, cnOnly, cnOnlyDNS, unknownCA, expired, notYet, interNoChain, interChain, selfSigned, client, otherClient *fakepeer.Leaf
}

func newTestPKI(t *testing.T) *testPKI {
	t.Helper()
	p, err := fakepeer.NewPKI(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	lo := []net.IP{net.ParseIP("127.0.0.1")}
	issue := func(name string, o fakepeer.LeafOpts) *fakepeer.Leaf {
		l, err := p.Issue(name, o)
		if err != nil {
			t.Fatal(err)
		}
		return l
	}
	now := time.Now()
	return &testPKI{PKI: p,
		good:         issue("good", fakepeer.LeafOpts{Issuer: p.Root, CN: "peer", DNS: []string{"peer.test"}, IPs: lo}),
		dnsOnly:      issue("dns-only", fakepeer.LeafOpts{Issuer: p.Root, CN: "peer", DNS: []string{"peer.test"}}),
		ipOnly:       issue("ip-only", fakepeer.LeafOpts{Issuer: p.Root, CN: "peer", IPs: lo}),
		wrongHost:    issue("wrong-host", fakepeer.LeafOpts{Issuer: p.Root, CN: "other", DNS: []string{"other.test"}}),
		cnOnly:       issue("cn-only", fakepeer.LeafOpts{Issuer: p.Root, CN: "127.0.0.1"}),
		cnOnlyDNS:    issue("cn-only-dns", fakepeer.LeafOpts{Issuer: p.Root, CN: "peer.test"}),
		unknownCA:    issue("unknown-ca", fakepeer.LeafOpts{Issuer: p.OtherCA, CN: "peer", IPs: lo}),
		expired:      issue("expired", fakepeer.LeafOpts{Issuer: p.Root, CN: "peer", IPs: lo, NotBefore: now.Add(-48 * time.Hour), NotAfter: now.Add(-24 * time.Hour)}),
		notYet:       issue("not-yet", fakepeer.LeafOpts{Issuer: p.Root, CN: "peer", IPs: lo, NotBefore: now.Add(24 * time.Hour), NotAfter: now.Add(48 * time.Hour)}),
		interNoChain: issue("inter-no-chain", fakepeer.LeafOpts{Issuer: p.Inter, CN: "peer", IPs: lo}),
		interChain:   issue("inter-chain", fakepeer.LeafOpts{Issuer: p.Inter, CN: "peer", IPs: lo, WithIssuer: true}),
		selfSigned:   issue("self-signed", fakepeer.LeafOpts{CN: "peer", IPs: lo}),
		client:       issue("client", fakepeer.LeafOpts{Issuer: p.Root, CN: "libredvt-client", Client: true}),
		otherClient:  issue("other-client", fakepeer.LeafOpts{Issuer: p.OtherCA, CN: "stranger", Client: true}),
	}
}

func srv(l *fakepeer.Leaf, minV, maxV uint16) *tls.Config {
	return &tls.Config{Certificates: []tls.Certificate{l.TLS}, MinVersion: minV, MaxVersion: maxV} // #nosec G402 -- test peer; old versions are negative controls
}

const echoAssocPlan = `
name: tls assoc
target: t
timeout: 3s
steps:
  - id: a
    type: associate
    contexts:
      - {abstract_syntax: "1.2.840.10008.1.1", transfer_syntaxes: ["1.2.840.10008.1.2"], expect: accepted}
`

// Verifies: DVT-SRS-100, DVT-SRS-101, DVT-SRS-102, DVT-SRS-107
func TestRealTLSVerification(t *testing.T) {
	k := newTestPKI(t)
	ca := &store.TLS{CAFile: k.Root.File}
	// Positive: a chain to the configured CA with a matching IP SAN.
	p := startPeer(t, srv(k.good, tls.VersionTLS12, 0), fakepeer.Script{})
	r := runReal(t, echoAssocPlan, peerTarget(p, ca), engine.SCP{})
	if r.Verdict != result.Pass {
		t.Fatalf("trusted peer: %s %s", r.Verdict, msgs(r))
	}
	x := findExchange(r.Steps[0].Evidence.Exchanges, engine.OpTLS)
	sum := sha256.Sum256(k.good.DER)
	if x == nil || x.TLS == nil || x.TLS.Peer == nil || x.TLS.Version != "TLSv1.3" || x.TLS.Cipher == "" || !x.TLS.Verified ||
		x.TLS.Peer.SHA256 != hex.EncodeToString(sum[:]) || !strings.Contains(x.TLS.Peer.Subject, "CN=peer") ||
		!strings.Contains(x.TLS.Peer.Issuer, "CN=test-root-ca") || strings.Join(x.TLS.Peer.SANDNS, ",") != "peer.test" ||
		strings.Join(x.TLS.Peer.SANIP, ",") != "127.0.0.1" || x.TLS.Peer.NotBefore == "" || x.TLS.Peer.NotAfter == "" {
		t.Fatalf("TLS evidence: %+v %+v", x, x.TLS)
	}
	if !strings.Contains(msgs(r), "TLS: TLSv1.3") || !strings.Contains(msgs(r), hex.EncodeToString(sum[:])) {
		t.Fatalf("TLS facts not in the messages: %s", msgs(r))
	}
	pass := map[string]struct {
		leaf *fakepeer.Leaf
		tl   store.TLS
	}{
		"DNS SAN via server_name": {k.dnsOnly, store.TLS{CAFile: k.Root.File, ServerName: "peer.test"}},
		"IP SAN":                  {k.ipOnly, store.TLS{CAFile: k.Root.File}},
		"chain with intermediate": {k.interChain, store.TLS{CAFile: k.Root.File}},
	}
	for name, c := range pass {
		p := startPeer(t, srv(c.leaf, tls.VersionTLS12, 0), fakepeer.Script{})
		tl := c.tl
		if r := runReal(t, echoAssocPlan, peerTarget(p, &tl), engine.SCP{}); r.Verdict != result.Pass {
			t.Errorf("%s: %s %s", name, r.Verdict, msgs(r))
		}
	}
	fail := map[string]struct {
		leaf *fakepeer.Leaf
		tl   store.TLS
		text string
	}{
		"wrong host":        {k.wrongHost, store.TLS{CAFile: k.Root.File}, "certificate verification failed"},
		"wrong server_name": {k.good, store.TLS{CAFile: k.Root.File, ServerName: "other.test"}, "certificate verification failed"},
		"name only in CN":   {k.cnOnly, store.TLS{CAFile: k.Root.File}, "certificate verification failed"},
		// OpenSSL never matches an IP against the CN, so a DNS name is needed
		// to prove the CN fallback is off (DVT-SRS-102).
		"DNS name only in CN": {k.cnOnlyDNS, store.TLS{CAFile: k.Root.File, ServerName: "peer.test"}, "certificate verification failed"},
		"unknown CA":          {k.unknownCA, store.TLS{CAFile: k.Root.File}, "certificate verification failed"},
		"expired":             {k.expired, store.TLS{CAFile: k.Root.File}, "expired"},
		"not yet valid":       {k.notYet, store.TLS{CAFile: k.Root.File}, "not yet valid"},
		"incomplete chain":    {k.interNoChain, store.TLS{CAFile: k.Root.File}, "certificate verification failed"},
		"self-signed leaf":    {k.selfSigned, store.TLS{CAFile: k.Root.File}, "certificate verification failed"},
		"system trust store":  {k.good, store.TLS{}, "certificate verification failed"},
	}
	for name, c := range fail {
		p := startPeer(t, srv(c.leaf, tls.VersionTLS12, 0), fakepeer.Script{})
		tl := c.tl
		r := runReal(t, echoAssocPlan, peerTarget(p, &tl), engine.SCP{})
		ev := r.Steps[0].Evidence
		if r.Verdict != result.Fail || len(ev.Errors) == 0 || ev.Errors[0].Kind != result.ProblemTLS || !strings.Contains(msgs(r), c.text) {
			t.Errorf("%s: %s %s", name, r.Verdict, msgs(r))
		}
		// Nothing but the handshake reached the peer.
		for _, cn := range p.Conns() {
			if cn.RQ != nil {
				t.Errorf("%s: the peer received an A-ASSOCIATE-RQ after a failed handshake", name)
			}
		}
	}
}

// Verifies: DVT-SRS-100
func TestRealTLSVersions(t *testing.T) {
	k := newTestPKI(t)
	for name, c := range map[string]struct {
		minV, maxV uint16
		targetMin  string
		want       result.Verdict
	}{
		"TLS 1.1 only server":        {tls.VersionTLS11, tls.VersionTLS11, "1.2", result.Fail},
		"TLS 1.0 only server":        {tls.VersionTLS10, tls.VersionTLS10, "1.2", result.Fail},
		"TLS 1.2 only server":        {tls.VersionTLS12, tls.VersionTLS12, "1.2", result.Pass},
		"TLS 1.3 only server":        {tls.VersionTLS13, tls.VersionTLS13, "1.2", result.Pass},
		"TLS 1.2 server, target 1.3": {tls.VersionTLS12, tls.VersionTLS12, "1.3", result.Fail},
	} {
		p := startPeer(t, srv(k.good, c.minV, c.maxV), fakepeer.Script{})
		r := runReal(t, echoAssocPlan, peerTarget(p, &store.TLS{CAFile: k.Root.File, MinVersion: c.targetMin}), engine.SCP{})
		if r.Verdict != c.want {
			t.Errorf("%s: %s %s", name, r.Verdict, msgs(r))
		}
		if c.want == result.Fail {
			for _, cn := range p.Conns() {
				if cn.RQ != nil || cn.Handshake == "" {
					t.Errorf("%s: handshake %q, RQ %v: plaintext or old-version traffic reached the peer", name, cn.Handshake, cn.RQ != nil)
				}
			}
		}
	}
}

// Verifies: DVT-SRS-104
func TestRealMutualTLS(t *testing.T) {
	k := newTestPKI(t)
	cfg := srv(k.good, tls.VersionTLS12, 0)
	cfg.ClientAuth = tls.RequireAndVerifyClientCert
	cfg.ClientCAs = fakepeer.Pool(k.Root)
	p := startPeer(t, cfg, fakepeer.Script{})
	pair := &store.TLS{CAFile: k.Root.File, ClientCertFile: k.client.CertFile, ClientKeyFile: k.client.KeyFile}
	r := runReal(t, echoAssocPlan, peerTarget(p, pair), engine.SCP{})
	if r.Verdict != result.Pass || len(p.Conns()) != 1 || !p.Conns()[0].ClientCert {
		t.Fatalf("configured pair: %s %s %+v", r.Verdict, msgs(r), p.Conns())
	}
	if r := runReal(t, echoAssocPlan, peerTarget(p, &store.TLS{CAFile: k.Root.File}), engine.SCP{}); r.Verdict != result.Fail {
		t.Fatalf("no client certificate: %s", r.Verdict)
	}
	other := &store.TLS{CAFile: k.Root.File, ClientCertFile: k.otherClient.CertFile, ClientKeyFile: k.otherClient.KeyFile}
	if r := runReal(t, echoAssocPlan, peerTarget(p, other), engine.SCP{}); r.Verdict != result.Fail {
		t.Fatalf("client certificate from another CA: %s", r.Verdict)
	}
	// A mismatched or missing key fails before the peer sees a connection.
	before := len(p.Conns())
	for name, tl := range map[string]*store.TLS{
		"mismatched key": {CAFile: k.Root.File, ClientCertFile: k.client.CertFile, ClientKeyFile: k.otherClient.KeyFile},
		"missing key":    {CAFile: k.Root.File, ClientCertFile: k.client.CertFile, ClientKeyFile: k.Dir + "/no.key"},
	} {
		r := runReal(t, echoAssocPlan, peerTarget(p, tl), engine.SCP{})
		if r.Verdict != result.Fail || r.Steps[0].Evidence.Errors[0].Kind != result.ProblemConfig {
			t.Errorf("%s: %s %s", name, r.Verdict, msgs(r))
		}
	}
	if n := len(p.Conns()); n != before {
		t.Fatalf("the peer saw %d connection(s) for bad key files", n-before)
	}
}

// Verifies: DVT-SRS-100, DVT-SRS-107
func TestRealV01EchoOverTLS(t *testing.T) {
	k := newTestPKI(t)
	p := startPeer(t, srv(k.good, tls.VersionTLS12, 0), fakepeer.Script{})
	src := "name: e\ntarget: t\ntimeout: 3s\nsteps:\n  - {id: e, type: echo}\n"
	r := runReal(t, src, peerTarget(p, &store.TLS{CAFile: k.Root.File}), engine.SCP{})
	if r.Verdict != result.Pass || !strings.Contains(msgs(r), "TLS: TLSv1.3") || !strings.Contains(msgs(r), "C-ECHO status 0x0000") {
		t.Fatalf("echo over TLS: %s %s", r.Verdict, msgs(r))
	}
	if c := p.Conns(); len(c) != 1 || c[0].PData == 0 {
		t.Fatalf("peer saw %+v", c)
	}
	p2 := startPeer(t, srv(k.wrongHost, tls.VersionTLS12, 0), fakepeer.Script{})
	r = runReal(t, src, peerTarget(p2, &store.TLS{CAFile: k.Root.File}), engine.SCP{})
	if r.Verdict != result.Fail || !strings.Contains(msgs(r), "TLS error") || !strings.Contains(msgs(r), "certificate verification failed") {
		t.Fatalf("echo to wrong host: %s %s", r.Verdict, msgs(r))
	}
	for _, c := range p2.Conns() {
		if c.PData != 0 || c.RQ != nil {
			t.Fatal("DICOM data reached a peer whose certificate failed")
		}
	}
}

func probePlan(mode, version string) string {
	s := "name: p\ntarget: t\ntimeout: 3s\nsteps:\n  - {id: p, type: tls_reject, mode: " + mode
	if version != "" {
		s += ", tls_version: \"" + version + "\""
	}
	return s + "}\n"
}

// Verifies: DVT-SRS-105, DVT-SRS-106
func TestRealTLSReject(t *testing.T) {
	k := newTestPKI(t)
	strict := srv(k.good, tls.VersionTLS12, 0)
	mtls := srv(k.good, tls.VersionTLS12, 0)
	mtls.ClientAuth = tls.RequireAndVerifyClientCert
	mtls.ClientCAs = fakepeer.Pool(k.Root)
	pair := &store.TLS{CAFile: k.Root.File, ClientCertFile: k.client.CertFile, ClientKeyFile: k.client.KeyFile}
	cases := map[string]struct {
		cfg     *tls.Config
		mode, v string
		want    string // refused or accepted
	}{
		"plaintext to TLS port":          {strict, "plaintext", "", "refused"},
		"plaintext accepted":             {nil, "plaintext", "", "accepted"},
		"TLS 1.1 to TLS 1.2+ server":     {strict, "tls_version", "1.1", "refused"},
		"TLS 1.0 to TLS 1.2+ server":     {strict, "tls_version", "1.0", "refused"},
		"TLS 1.1 accepted":               {srv(k.good, tls.VersionTLS11, 0), "tls_version", "1.1", "accepted"},
		"TLS 1.0 accepted":               {srv(k.good, tls.VersionTLS10, 0), "tls_version", "1.0", "accepted"},
		"no client cert, required":       {mtls, "no_client_cert", "", "refused"},
		"no client cert, not required":   {strict, "no_client_cert", "", "accepted"},
		"no client cert, TLS 1.2 server": {func() *tls.Config { c := mtls.Clone(); c.MaxVersion = tls.VersionTLS12; return c }(), "no_client_cert", "", "refused"},
	}
	for name, c := range cases {
		p := startPeer(t, c.cfg, fakepeer.Script{})
		r := runReal(t, probePlan(c.mode, c.v), peerTarget(p, pair), engine.SCP{})
		ev := r.Steps[0].Evidence
		want := result.Pass
		if c.want == "accepted" {
			want = result.Fail
		}
		if r.Verdict != want || len(ev.Assertions) != 1 || ev.Assertions[0].Actual != c.want {
			t.Errorf("%s: %s %+v %s", name, r.Verdict, ev.Assertions, msgs(r))
		}
		// DVT-SRS-106: no client certificate, no P-DATA, no release, ever.
		for _, cn := range p.Conns() {
			if cn.ClientCert || cn.PData != 0 || strings.ContainsRune(string(cn.PDUs), rune(fakepeer.PDUReleaseRQ)) {
				t.Errorf("%s: the probe sent more than allowed: %+v", name, cn)
			}
		}
	}
}

// Verifies: DVT-SRS-108
func TestRealSCPTLSDuringMove(t *testing.T) {
	k := newTestPKI(t)
	scpLeaf, err := k.Issue("libredvt-scp", fakepeer.LeafOpts{Issuer: k.Root, CN: "libredvt", IPs: []net.IP{net.ParseIP("127.0.0.1")}})
	if err != nil {
		t.Fatal(err)
	}
	e := realEngines(t)
	e.Version = "0.2.0"
	clientCfg := func(leaf *fakepeer.Leaf) *tls.Config {
		c := &tls.Config{RootCAs: fakepeer.Pool(k.Root), ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}
		if leaf != nil {
			c.Certificates = []tls.Certificate{leaf.TLS}
		}
		return c
	}
	cases := map[string]struct {
		leaf     *fakepeer.Leaf
		mode     string
		accepted bool
	}{
		"client certificate from the target's CA": {k.client, "required", true},
		"no client certificate":                   {nil, "required", false},
		"no client certificate, optional":         {nil, "optional", true},
		"client certificate from another CA":      {k.otherClient, "required", false},
	}
	for name, c := range cases {
		scpPort := freePort(t)
		p := startPeer(t, srv(k.good, tls.VersionTLS12, 0), fakepeer.Script{Move: &fakepeer.MoveScript{
			Addr: net.JoinHostPort("127.0.0.1", strconv.Itoa(scpPort)), TLS: clientCfg(c.leaf), CallingAET: "FAKEPEER"}})
		peer := engine.Peer{Host: "127.0.0.1", Port: p.Port, CalledAET: "FAKEPEER", CallingAET: "LIBREDVT", Timeout: 5 * time.Second,
			TLS: &engine.TLS{CAFile: k.Root.File, MoveClientCert: c.mode}}
		res, _, err := e.Move(context.Background(), peer, engine.SCP{AETitle: "LIBREDVT", Port: scpPort, TLSCert: scpLeaf.CertFile, TLSKey: scpLeaf.KeyFile},
			t.TempDir(), []string{"1.2.3"}, 0)
		if err != nil || !res.Associated || res.TLS == nil {
			t.Errorf("%s: move did not run over TLS: %v %+v", name, err, res)
			continue
		}
		cn := p.Conns()
		if len(cn) != 1 || cn[0].Sub == nil {
			t.Errorf("%s: peer %+v", name, cn)
			continue
		}
		got := cn[0].Sub.Answer == fakepeer.PDUAssociateAC
		if got != c.accepted || len(res.SCPConnections) != 1 {
			t.Errorf("%s: sub-association accepted %v (want %v), SCP connections %+v, sub %+v", name, got, c.accepted, res.SCPConnections, cn[0].Sub)
			continue
		}
		sc := res.SCPConnections[0]
		if c.accepted && (sc.Outcome != "completed" || sc.TLS == nil) {
			t.Errorf("%s: SCP record %+v", name, sc)
		}
		if c.accepted && c.leaf != nil && !strings.Contains(sc.TLS.ClientSubject, "CN=libredvt-client") {
			t.Errorf("%s: client subject %+v", name, sc.TLS)
		}
	}
	// Without the SCP certificate nothing is sent to the target.
	p := startPeer(t, srv(k.good, tls.VersionTLS12, 0), fakepeer.Script{})
	peer := engine.Peer{Host: "127.0.0.1", Port: p.Port, CalledAET: "FAKEPEER", CallingAET: "LIBREDVT", Timeout: 5 * time.Second,
		TLS: &engine.TLS{CAFile: k.Root.File}}
	res, _, err := e.Move(context.Background(), peer, engine.SCP{AETitle: "LIBREDVT", Port: freePort(t)}, t.TempDir(), []string{"1.2.3"}, 0)
	if err != nil || res.Associated || !strings.Contains(res.Error, "TLS") || len(p.Conns()) != 0 {
		t.Fatalf("move without SCP certificate: %v %+v, peer saw %d connection(s)", err, res, len(p.Conns()))
	}
}
