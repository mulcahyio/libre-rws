package fakepeer

import (
	"bytes"
	"crypto/sha256"
	"crypto/tls"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// This file is LibreDVT's deterministic DICOMweb test server, written in
// Go (net/http, mime/multipart, encoding/json), so the DICOMweb client
// in the Python worker (http.client, its own multipart parser) is never
// checked against itself. It is test infrastructure only: never shipped,
// only tests import this package (architecture test). Faults are chosen
// per test, so each malformed-response control is one switch.

// WebInstance is one DICOM object the server knows: its UIDs, a few
// attributes (8-hex tag -> value) and its Part 10 bytes.
type WebInstance struct {
	Study, Series, SOP, Class, TS string
	Attrs                         map[string]string
	Bytes                         []byte
}

// WebScript configures the server.
type WebScript struct {
	Instances []WebInstance   // stored at start (served by QIDO and WADO)
	Known     []WebInstance   // objects STOW may receive, identified by their bytes
	Faults    map[string]bool // misbehaviour switches, see the constants below
	Other     string          // URL of another origin (redirect and Retrieve URL faults)
	Delay     time.Duration   // delay before each answer (concurrency checks)
}

// Faults.
const (
	FQidoDropFirst    = "qido-drop-first"
	FQidoCTypeJSON    = "qido-ctype-json"
	FQidoBadJSON      = "qido-bad-json"
	FQidoHTML         = "qido-html"
	FQido204          = "qido-204"
	FQido500          = "qido-500"
	FQidoNoUniqueKey  = "qido-no-unique-key"
	FQidoWrongPatient = "qido-wrong-patient"
	FQidoIgnoreLimit  = "qido-ignore-limit"
	FQidoEndless      = "qido-endless"
	FQidoHugeJSON     = "qido-huge-json"  // a valid JSON array of about 200 KiB
	FQidoStreamBig    = "qido-stream-big" // about 2 MiB of body
	FEchoAuth         = "echo-auth"       // a 500 page that quotes the Authorization header
	FRedirect         = "redirect"        // 302 to Other on every request
	FWadoTruncated    = "wado-truncated"  // no closing delimiter
	FWadoBadBoundary  = "wado-bad-boundary"
	FWadoOctet        = "wado-octet" // first part application/octet-stream
	FWadoGarbagePart  = "wado-garbage-part"
	FWadoBoundaryData = "wado-boundary-in-data" // boundary text occurs inside the parts' data
	FWadoDropOne      = "wado-drop-one"
	FWadoExtraOne     = "wado-extra-one"
	FWadoDuplicate    = "wado-duplicate"
	FWadoIgnoreTS     = "wado-ignore-ts"   // serve the stored syntax, label it as requested
	FWadoTSHeader     = "wado-ts-header"   // part header names another transfer syntax
	FWadoAlterPixel   = "wado-alter-pixel" // one byte inside the pixel data of each part changed
	FWado206          = "wado-206"
	FWado404          = "wado-404"
	FWado503          = "wado-503"
	FWadoNotMultipart = "wado-not-multipart"
	FWadoManyParts    = "wado-many-parts" // every instance three times (part limit)
	FMetaDropOne      = "meta-drop-one"
	FMetaWrongPatient = "meta-wrong-patient"
	FStowFailOne      = "stow-fail-one" // 202, first instance failed 0xA700
	FStow409          = "stow-409"
	FStowOmitOne      = "stow-omit-one"
	FStowExtraRef     = "stow-extra-ref"
	FStowWrongClass   = "stow-wrong-class"
	FStow200Failure   = "stow-200-with-failure"
	FStowWarning      = "stow-warning" // 202, Warning Reason 0xB000 on the first instance
	FStowEmpty200     = "stow-empty-200"
	FStowXML          = "stow-xml"
	FStowJSONArray    = "stow-json-array"
	FStowBadJSON      = "stow-bad-json"
	FStowOtherOrigin  = "stow-retrieve-other-origin"
	FStow307          = "stow-307"
	FStowAlterStored  = "stow-alter-stored" // stores a changed copy (verify_retrieve must notice)
	FHTTPMalformed    = "http-malformed"    // a reply that is not HTTP
	FTruncatedBody    = "truncated-body"    // Content-Length larger than the body sent
)

// WebRequest is what the server saw for one request.
type WebRequest struct {
	Method, Path, RawQuery string
	Authorization          string // header value as received
	Accept                 string
	PartSHA256             []string // STOW: SHA-256 of each received part
	ContentType            string
}

// Web is a running DICOMweb test server. URL is its base URL
// (…/dicom-web).
type Web struct {
	URL       string
	Port      int
	ln        net.Listener
	srv       *http.Server
	s         WebScript
	mu        sync.Mutex
	stored    []WebInstance
	reqs      []WebRequest
	active    atomic.Int32
	maxActive atomic.Int32
	conns     atomic.Int32
}

// StartWeb starts the server on 127.0.0.1 (TLS when cfg is not nil).
func StartWeb(cfg *tls.Config, s WebScript) (*Web, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	w := &Web{ln: ln, s: s, stored: append([]WebInstance(nil), s.Instances...)}
	w.Port = ln.Addr().(*net.TCPAddr).Port
	scheme := "http"
	if cfg != nil {
		scheme = "https"
		ln = tls.NewListener(ln, cfg)
	}
	w.URL = fmt.Sprintf("%s://127.0.0.1:%d/dicom-web", scheme, w.Port)
	w.srv = &http.Server{Handler: w, ReadHeaderTimeout: 10 * time.Second, ConnState: w.connState,
		ErrorLog: nil}
	if s.Faults[FHTTPMalformed] {
		go w.garbage(ln)
		return w, nil
	}
	go func() { _ = w.srv.Serve(ln) }()
	return w, nil
}

func (w *Web) garbage(ln net.Listener) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		w.conns.Add(1)
		go func() {
			defer c.Close()
			buf := make([]byte, 4096)
			_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
			_, _ = c.Read(buf)
			_, _ = c.Write([]byte("SSH-2.0-not-http\r\n\r\n"))
		}()
	}
}

func (w *Web) connState(_ net.Conn, st http.ConnState) {
	switch st {
	case http.StateNew:
		w.conns.Add(1)
		n := w.active.Add(1)
		for {
			m := w.maxActive.Load()
			if n <= m || w.maxActive.CompareAndSwap(m, n) {
				break
			}
		}
	case http.StateClosed, http.StateHijacked:
		w.active.Add(-1)
	}
}

// Close stops the server.
func (w *Web) Close() {
	_ = w.srv.Close()
	_ = w.ln.Close()
}

// Requests returns what the server saw, in order.
func (w *Web) Requests() []WebRequest {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]WebRequest(nil), w.reqs...)
}

// MaxConcurrent is the largest number of open connections at one time.
func (w *Web) MaxConcurrent() int { return int(w.maxActive.Load()) }

// Connections is the number of connections accepted.
func (w *Web) Connections() int { return int(w.conns.Load()) }

// Stored returns the instances the server holds now.
func (w *Web) Stored() []WebInstance {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]WebInstance(nil), w.stored...)
}

func (w *Web) fault(f string) bool { return w.s.Faults[f] }

func (w *Web) ServeHTTP(rw http.ResponseWriter, r *http.Request) {
	rec := WebRequest{Method: r.Method, Path: r.URL.Path, RawQuery: r.URL.RawQuery, Authorization: r.Header.Get("Authorization"),
		Accept: r.Header.Get("Accept"), ContentType: r.Header.Get("Content-Type")}
	var body []byte
	if r.Method == http.MethodPost {
		body, _ = io.ReadAll(io.LimitReader(r.Body, 64<<20))
		rec.PartSHA256 = partHashes(r.Header.Get("Content-Type"), body)
	}
	w.mu.Lock()
	w.reqs = append(w.reqs, rec)
	w.mu.Unlock()
	if w.s.Delay > 0 {
		time.Sleep(w.s.Delay)
	}
	if w.fault(FRedirect) || (w.fault(FStow307) && r.Method == http.MethodPost) {
		code := http.StatusFound
		if r.Method == http.MethodPost {
			code = http.StatusTemporaryRedirect
		}
		rw.Header().Set("Location", w.s.Other+r.URL.Path)
		rw.WriteHeader(code)
		return
	}
	if w.fault(FEchoAuth) {
		rw.Header().Set("Content-Type", "text/plain")
		rw.WriteHeader(http.StatusInternalServerError)
		_, _ = fmt.Fprintf(rw, "access denied for %s\n", rec.Authorization)
		return
	}
	if w.fault(FTruncatedBody) {
		rw.Header().Set("Content-Type", "application/dicom+json")
		rw.Header().Set("Content-Length", "5000")
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("[{\"0020000D\":"))
		return
	}
	rest, ok := strings.CutPrefix(r.URL.Path, "/dicom-web")
	if !ok {
		http.NotFound(rw, r)
		return
	}
	seg := strings.Split(strings.Trim(rest, "/"), "/")
	switch {
	case r.Method == http.MethodPost && seg[0] == "studies" && len(seg) <= 2:
		w.stow(rw, r, body)
	case r.Method != http.MethodGet:
		rw.WriteHeader(http.StatusMethodNotAllowed)
	case isSearch(seg):
		w.qido(rw, r, seg)
	case len(seg) >= 2 && seg[0] == "studies" && seg[len(seg)-1] == "metadata":
		w.metadata(rw, seg[:len(seg)-1])
	case len(seg) >= 2 && seg[0] == "studies":
		w.wado(rw, r, seg)
	default:
		http.NotFound(rw, r)
	}
}

// --- QIDO-RS

func isSearch(seg []string) bool {
	switch len(seg) {
	case 1:
		return seg[0] == "studies" || seg[0] == "series" || seg[0] == "instances"
	case 3:
		return seg[0] == "studies" && (seg[2] == "series" || seg[2] == "instances")
	case 5:
		return seg[0] == "studies" && seg[2] == "series" && seg[4] == "instances"
	}
	return false
}

var keywordTags = map[string]string{"PatientID": "00100020", "StudyInstanceUID": "0020000D", "SeriesInstanceUID": "0020000E",
	"SOPInstanceUID": "00080018", "SOPClassUID": "00080016", "Modality": "00080060", "StudyDate": "00080020"}

var tagVR = map[string]string{"00100020": "LO", "0020000D": "UI", "0020000E": "UI", "00080018": "UI", "00080016": "UI", "00080060": "CS", "00080020": "DA"}

func el(tag, v string) map[string]any {
	return map[string]any{"vr": tagVR[tag], "Value": []string{v}}
}

func (w *Web) object(level string, in WebInstance) map[string]any {
	o := map[string]any{"0020000D": el("0020000D", in.Study)}
	switch level {
	case "studies":
		for _, t := range []string{"00100020", "00080020"} {
			if v, ok := in.Attrs[t]; ok {
				o[t] = el(t, v)
			}
		}
	case "series":
		o["0020000E"] = el("0020000E", in.Series)
		if v, ok := in.Attrs["00080060"]; ok {
			o["00080060"] = el("00080060", v)
		}
	default:
		o["0020000E"] = el("0020000E", in.Series)
		o["00080018"] = el("00080018", in.SOP)
		o["00080016"] = el("00080016", in.Class)
	}
	return o
}

func (w *Web) qido(rw http.ResponseWriter, r *http.Request, seg []string) {
	level := seg[len(seg)-1]
	q := r.URL.Query()
	w.mu.Lock()
	all := append([]WebInstance(nil), w.stored...)
	w.mu.Unlock()
	var objs []map[string]any
	seen := map[string]bool{}
	for _, in := range all {
		if len(seg) >= 2 && in.Study != seg[1] || len(seg) >= 4 && in.Series != seg[3] {
			continue
		}
		vals := map[string]string{"0020000D": in.Study, "0020000E": in.Series, "00080018": in.SOP, "00080016": in.Class}
		for k, v := range in.Attrs {
			vals[k] = v
		}
		match := true
		for k, vs := range q {
			switch k {
			case "limit", "offset", "includefield", "fuzzymatching":
				continue
			}
			tag := keywordTags[k]
			if tag == "" {
				tag = strings.ToUpper(k)
			}
			if vals[tag] != vs[0] {
				match = false
			}
		}
		if !match {
			continue
		}
		key := map[string]string{"studies": in.Study, "series": in.Series, "instances": in.SOP}[level]
		if seen[key] {
			continue
		}
		seen[key] = true
		objs = append(objs, w.object(level, in))
	}
	sort.Slice(objs, func(i, j int) bool {
		return fmt.Sprint(objs[i]["0020000D"], objs[i]["00080018"], objs[i]["0020000E"]) < fmt.Sprint(objs[j]["0020000D"], objs[j]["00080018"], objs[j]["0020000E"])
	})
	limit, _ := strconv.Atoi(q.Get("limit"))
	offset, _ := strconv.Atoi(q.Get("offset"))
	switch {
	case w.fault(FQidoEndless) && limit > 0:
		page := make([]map[string]any, 0, limit)
		for i := 0; i < limit; i++ {
			page = append(page, map[string]any{"0020000D": el("0020000D", fmt.Sprintf("1.2.3.%d", offset+i))})
		}
		objs = page
	case limit > 0 && !w.fault(FQidoIgnoreLimit):
		if offset > len(objs) {
			offset = len(objs)
		}
		objs = objs[offset:min(offset+limit, len(objs))]
	}
	if w.fault(FQidoDropFirst) && len(objs) > 0 {
		objs = objs[1:]
	}
	for _, o := range objs {
		if w.fault(FQidoNoUniqueKey) {
			delete(o, map[string]string{"studies": "0020000D", "series": "0020000E", "instances": "00080018"}[level])
		}
		if w.fault(FQidoWrongPatient) {
			if _, ok := o["00100020"]; ok {
				o["00100020"] = el("00100020", "OTHER-PATIENT")
			}
		}
	}
	switch {
	case w.fault(FQido500):
		rw.Header().Set("Content-Type", "text/plain")
		rw.WriteHeader(http.StatusInternalServerError)
		_, _ = rw.Write([]byte("internal error\n"))
		return
	case w.fault(FQido204), len(objs) == 0:
		rw.WriteHeader(http.StatusNoContent)
		return
	case w.fault(FQidoHTML):
		rw.Header().Set("Content-Type", "text/html")
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte("<html><body>Login required</body></html>"))
		return
	case w.fault(FQidoStreamBig):
		rw.Header().Set("Content-Type", "application/dicom+json")
		rw.WriteHeader(http.StatusOK)
		chunk := bytes.Repeat([]byte(" "), 64<<10)
		_, _ = rw.Write([]byte("["))
		for i := 0; i < 32; i++ {
			_, _ = rw.Write(chunk)
		}
		_, _ = rw.Write([]byte("]"))
		return
	case w.fault(FQidoHugeJSON):
		for len(objs) < 2000 {
			objs = append(objs, objs[0])
		}
	}
	ct := "application/dicom+json"
	if w.fault(FQidoCTypeJSON) {
		ct = "application/json"
	}
	b, _ := json.Marshal(objs)
	if w.fault(FQidoBadJSON) {
		b = b[:len(b)/2]
	}
	rw.Header().Set("Content-Type", ct)
	rw.WriteHeader(http.StatusOK)
	_, _ = rw.Write(b)
}

// --- WADO-RS

func (w *Web) select3(seg []string) []WebInstance {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []WebInstance
	for _, in := range w.stored {
		if in.Study != seg[1] || len(seg) >= 4 && in.Series != seg[3] || len(seg) >= 6 && in.SOP != seg[5] {
			continue
		}
		out = append(out, in)
	}
	return out
}

func (w *Web) wado(rw http.ResponseWriter, r *http.Request, seg []string) {
	switch {
	case w.fault(FWado404):
		http.NotFound(rw, r)
		return
	case w.fault(FWado503):
		rw.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	ins := w.select3(seg)
	if len(ins) == 0 {
		http.NotFound(rw, r)
		return
	}
	_, params, _ := mime.ParseMediaType(r.Header.Get("Accept"))
	want := params["transfer-syntax"]
	if w.fault(FWadoDropOne) {
		ins = ins[1:]
	}
	if w.fault(FWadoExtraOne) {
		for _, k := range w.s.Known {
			if k.SOP != ins[0].SOP && !hasSOP(ins, k.SOP) {
				ins = append(ins, k)
				break
			}
		}
	}
	if w.fault(FWadoDuplicate) {
		ins = append(ins, ins[0])
	}
	if w.fault(FWadoManyParts) {
		ins = append(append(ins, ins...), ins...)
	}
	boundary := "fakeweb-boundary-7f3a"
	var b bytes.Buffer
	for i, in := range ins {
		ts := in.TS
		if want != "" && want != "*" && want != in.TS {
			if !w.fault(FWadoIgnoreTS) {
				rw.WriteHeader(http.StatusNotAcceptable)
				return
			}
			ts = want // the label claims what was asked; the bytes are unchanged
		}
		if w.fault(FWadoTSHeader) {
			ts = "1.2.840.10008.1.2"
		}
		ct := "application/dicom; transfer-syntax=" + ts
		data := in.Bytes
		switch {
		case w.fault(FWadoOctet) && i == 0:
			ct = "application/octet-stream"
		case w.fault(FWadoGarbagePart) && i == 0:
			data = bytes.Repeat([]byte{0xde, 0xad, 0xbe, 0xef}, 64)
		case w.fault(FWadoAlterPixel):
			data = append([]byte(nil), data...)
			data[len(data)*3/4] ^= 0xff // inside the pixel data of every fixture
		case w.fault(FWadoBoundaryData):
			data = withBoundaryText(data, in.TS, boundary)
		}
		fmt.Fprintf(&b, "--%s\r\nContent-Type: %s\r\n\r\n", boundary, ct)
		b.Write(data)
		b.WriteString("\r\n")
	}
	if !w.fault(FWadoTruncated) {
		fmt.Fprintf(&b, "--%s--\r\n", boundary)
	} else {
		b.Truncate(b.Len() - 2) // inside the last part, no closing delimiter
	}
	hdrBoundary := boundary
	if w.fault(FWadoBadBoundary) {
		hdrBoundary = "another-boundary"
	}
	ct := fmt.Sprintf(`multipart/related; type="application/dicom"; boundary=%s`, hdrBoundary)
	if w.fault(FWadoNotMultipart) {
		ct = "application/dicom"
	}
	rw.Header().Set("Content-Type", ct)
	if w.fault(FWado206) {
		rw.WriteHeader(http.StatusPartialContent)
	} else {
		rw.WriteHeader(http.StatusOK)
	}
	_, _ = rw.Write(b.Bytes())
}

func hasSOP(ins []WebInstance, sop string) bool {
	for _, x := range ins {
		if x.SOP == sop {
			return true
		}
	}
	return false
}

func (w *Web) metadata(rw http.ResponseWriter, seg []string) {
	ins := w.select3(seg)
	if len(ins) == 0 {
		rw.WriteHeader(http.StatusNotFound)
		return
	}
	if w.fault(FMetaDropOne) {
		ins = ins[1:]
	}
	var objs []map[string]any
	for _, in := range ins {
		o := w.object("instances", in)
		for t, v := range in.Attrs {
			if t == "00100020" && w.fault(FMetaWrongPatient) {
				v = "OTHER-PATIENT"
			}
			o[t] = el(t, v)
		}
		objs = append(objs, o)
	}
	b, _ := json.Marshal(objs)
	rw.Header().Set("Content-Type", "application/dicom+json")
	rw.WriteHeader(http.StatusOK)
	_, _ = rw.Write(b)
}

// --- STOW-RS

func partHashes(ct string, body []byte) []string {
	_, params, err := mime.ParseMediaType(ct)
	if err != nil {
		return nil
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var out []string
	for {
		p, err := mr.NextPart()
		if err != nil {
			return out
		}
		b, _ := io.ReadAll(p)
		sum := sha256.Sum256(b)
		out = append(out, hex.EncodeToString(sum[:]))
	}
}

func sq(items []map[string]any) map[string]any { return map[string]any{"vr": "SQ", "Value": items} }

func (w *Web) stow(rw http.ResponseWriter, r *http.Request, body []byte) {
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || params["boundary"] == "" {
		rw.WriteHeader(http.StatusUnsupportedMediaType)
		return
	}
	mr := multipart.NewReader(bytes.NewReader(body), params["boundary"])
	var refs, failed []map[string]any
	ok := 0
	for i := 0; ; i++ {
		p, err := mr.NextPart()
		if err != nil {
			break
		}
		data, _ := io.ReadAll(p)
		sum := sha256.Sum256(data)
		var in *WebInstance
		for k := range w.s.Known {
			if s := sha256.Sum256(w.s.Known[k].Bytes); s == sum {
				in = &w.s.Known[k]
			}
		}
		if in == nil {
			failed = append(failed, map[string]any{"00081197": map[string]any{"vr": "US", "Value": []int{0xC000}}})
			continue
		}
		if (w.fault(FStowFailOne) && i == 0) || w.fault(FStow409) {
			failed = append(failed, map[string]any{"00081150": el("00080016", in.Class), "00081155": el("00080018", in.SOP),
				"00081197": map[string]any{"vr": "US", "Value": []int{0xA700}}})
			continue
		}
		cp := *in
		if w.fault(FStowAlterStored) {
			cp.Bytes = append([]byte(nil), cp.Bytes...)
			cp.Bytes[len(cp.Bytes)*3/4] ^= 0xff
		}
		w.mu.Lock()
		replaced := false
		for k := range w.stored {
			if w.stored[k].SOP == cp.SOP {
				w.stored[k], replaced = cp, true
			}
		}
		if !replaced {
			w.stored = append(w.stored, cp)
		}
		w.mu.Unlock()
		ok++
		if w.fault(FStow200Failure) && i == 0 {
			failed = append(failed, map[string]any{"00081150": el("00080016", in.Class), "00081155": el("00080018", in.SOP),
				"00081197": map[string]any{"vr": "US", "Value": []int{0xC000}}})
		}
		if w.fault(FStowOmitOne) && i == 0 {
			continue
		}
		url := w.URL + "/studies/" + in.Study + "/series/" + in.Series + "/instances/" + in.SOP
		if w.fault(FStowOtherOrigin) {
			url = w.s.Other + "/studies/" + in.Study
		}
		class := in.Class
		if w.fault(FStowWrongClass) && i == 0 {
			class = "1.2.840.10008.5.1.4.1.1.7"
		}
		ref := map[string]any{"00081150": el("00080016", class), "00081155": el("00080018", in.SOP),
			"00081190": map[string]any{"vr": "UR", "Value": []string{url}}}
		if w.fault(FStowWarning) && i == 0 {
			ref["00081196"] = map[string]any{"vr": "US", "Value": []int{0xB000}}
		}
		refs = append(refs, ref)
	}
	if w.fault(FStowExtraRef) {
		refs = append(refs, map[string]any{"00081150": el("00080016", "1.2.840.10008.5.1.4.1.1.2"), "00081155": el("00080018", "1.2.3.4.5.6.7")})
	}
	resp := map[string]any{}
	if len(refs) > 0 {
		resp["00081199"] = sq(refs)
	}
	if len(failed) > 0 {
		resp["00081198"] = sq(failed)
	}
	status := http.StatusOK
	switch {
	case ok == 0 && len(failed) > 0:
		status = http.StatusConflict
	case len(failed) > 0 && !w.fault(FStow200Failure), w.fault(FStowWarning):
		status = http.StatusAccepted
	}
	switch {
	case w.fault(FStowEmpty200):
		rw.WriteHeader(http.StatusOK)
		return
	case w.fault(FStowXML):
		rw.Header().Set("Content-Type", "application/dicom+xml")
		rw.WriteHeader(http.StatusOK)
		_, _ = rw.Write([]byte(`<?xml version="1.0"?><NativeDicomModel/>`))
		return
	}
	var b []byte
	if w.fault(FStowJSONArray) {
		b, _ = json.Marshal([]any{resp})
	} else {
		b, _ = json.Marshal(resp)
	}
	if w.fault(FStowBadJSON) {
		b = b[:len(b)/2]
	}
	rw.Header().Set("Content-Type", "application/dicom+json")
	rw.WriteHeader(status)
	_, _ = rw.Write(b)
}

// withBoundaryText appends a Data Set Trailing Padding element (FFFC,FFFC)
// whose value holds the multipart boundary in forms that are not
// delimiters: after CRLF but followed by other text, after CRLF and
// spaces but followed by text, and without a preceding CRLF. A strict
// parser keeps the part whole; one that splits on the bare boundary text
// or accepts any CRLF--boundary cuts the part. Pixel data and UIDs are
// unchanged. Files in big-endian or deflated syntaxes are returned as
// they are.
func withBoundaryText(data []byte, ts, boundary string) []byte {
	val := []byte("\r\n--" + boundary + "X not a delimiter\r\n--" + boundary + "  x\r\nab--" + boundary + "--\r\nend")
	if len(val)%2 == 1 {
		val = append(val, 0)
	}
	out := append([]byte(nil), data...)
	out = append(out, 0xfc, 0xff, 0xfc, 0xff)
	switch ts {
	case "1.2.840.10008.1.2.2", "1.2.840.10008.1.2.1.99":
		return data
	case "1.2.840.10008.1.2": // implicit VR little endian
		out = binary.LittleEndian.AppendUint32(out, uint32(len(val))) // #nosec G115 -- short constant text
	default: // explicit VR little endian (native and encapsulated)
		out = append(out, 'O', 'B', 0, 0)
		out = binary.LittleEndian.AppendUint32(out, uint32(len(val))) // #nosec G115 -- short constant text
	}
	return append(out, val...)
}
