package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/status"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/licensing"
)

const (
	wStudy   = "1.2.826.0.1.3680043.10.999.91"
	ctClass  = "1.2.840.10008.5.1.4.1.1.2"
	explicit = "1.2.840.10008.1.2.1"
)

// wiringImage is a small synthetic CT image.
func wiringImage(pid, name, series, sop string, n int) *godicom.Dataset {
	ds := godicom.NewDataset()
	set := func(kw, v string) { _ = ds.SetString(godicom.MustTag(kw), v) }
	set("SOPClassUID", ctClass)
	set("SOPInstanceUID", sop)
	set("StudyInstanceUID", wStudy)
	set("SeriesInstanceUID", series)
	set("PatientID", pid)
	set("PatientName", name)
	set("StudyDate", "20260115")
	set("AccessionNumber", "ACC-WIRE")
	set("Modality", "CT")
	set("SeriesNumber", "1")
	for kw, v := range map[string]int{"InstanceNumber": n, "Rows": 4, "Columns": 4, "BitsAllocated": 16, "BitsStored": 12,
		"HighBit": 11, "PixelRepresentation": 0, "SamplesPerPixel": 1} {
		_ = ds.SetInt(godicom.MustTag(kw), v)
	}
	set("PhotometricInterpretation", "MONOCHROME2")
	ds.Set(godicom.NewDataElement(godicom.MustTag("PixelData"), godicom.VROW, make([]byte, 32)))
	return ds
}

// The study query, the send and the configuration edit work through the
// real wiring of build(): the real runtime, DICOM gateway (listening),
// index, store, reloader, overlay file and loader. The test runs in source
// builds and, with `-tags official`, in builds that enforce a licence.
//
// It shows end to end that:
//   - images stored over DICOM are found by GET /control/v1/studies, and an
//     instance held as a patient conflict is not;
//   - a send needs the plan's digest, goes to the configured destination
//     over DICOM, and delivers the indexed instances only;
//   - a configuration edit is written to the overlay file, never to
//     config.yaml, is in force in the gateway at once and again after a
//     restart, and a rollback puts the previous settings back;
//   - a setting that needs a restart is refused;
//   - an unlicensed, unacknowledged official build blocks the edit (423).
//
// Verifies: DCM-SRS-091, DCM-SRS-092, DCM-SRS-093, DCM-SRS-094, DCM-SRS-095, DCM-SRS-096, DCM-SRS-060
func TestControlDataAndConfigWiring(t *testing.T) {
	const token = "wiring-admin-token-9c27ab"
	for _, v := range []string{"LIBRE_DCM_ADMIN_TOKEN", "LIBRE_DCM_REMOTE_AES", "LIBRE_DCM_ALLOWED_AES", "LIBRE_DCM_CONTROL_BACKUP_DIR", "LIBRE_DCM_CONTROL_OVERLAY_FILE"} {
		t.Setenv(v, "")
	}
	t.Setenv("LIBREKEY_HOST_ID", "wiring-host")
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	// A destination that records what it receives.
	var mu sync.Mutex
	got := map[string]string{}
	destLn, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() {
		_ = ae.Serve(ctx, destLn, ae.ServerConfig{AETitle: "DEST", AcceptedAbstractSyntaxes: ae.AllStorageSOPClasses,
			OnCStore: func(_ context.Context, req ae.StoreRequest) uint16 {
				mu.Lock()
				defer mu.Unlock()
				pid, _ := req.Data.GetString(godicom.MustTag("PatientID"))
				got[req.AffectedSOPInstanceUID] = pid
				return status.Success
			}})
	}()
	destPort := destLn.Addr().(*net.TCPAddr).Port
	received := func() string {
		mu.Lock()
		defer mu.Unlock()
		var out []string
		for sop, pid := range got {
			out = append(out, strings.TrimPrefix(sop, wStudy)+"="+pid)
		}
		sortStrings(out)
		return strings.Join(out, " ")
	}

	dir := t.TempDir()
	cfgPath, overlay := filepath.Join(dir, "config.yaml"), filepath.Join(dir, "data", "config-overlay.yaml")
	cfgText := `admin:
  addr: "127.0.0.1:0"
  node_id: wiring-node
  token: ` + token + `
dicom:
  ae_title: WIRING
  listen: "127.0.0.1:0"
  allowed_aes: [MODALITY]
  remote_aes:
    DEST: {host: 127.0.0.1, port: ` + fmt.Sprint(destPort) + `}
storage:
  driver: fs
  path: ` + filepath.Join(dir, "blobs") + `
index:
  driver: sqlite
  dsn: ` + filepath.Join(dir, "index.db") + `
events:
  driver: log
license:
  file: ` + filepath.Join(dir, "license", "license.lic") + `
  data_dir: ` + filepath.Join(dir, "license") + `
control:
  overlay_file: ` + overlay + `
`
	if err := os.WriteFile(cfgPath, []byte(cfgText), 0o600); err != nil {
		t.Fatal(err)
	}
	a, err := build(cfgPath)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	closed := false
	defer func() {
		if !closed {
			a.close()
		}
	}()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	gwCtx, stopGW := context.WithCancel(ctx)
	defer stopGW()
	gwDone := make(chan struct{})
	go func() { _ = a.gw.Serve(gwCtx, ln); close(gwDone) }()

	h := a.rt.AdminHandler()
	seq := 0
	call := func(method, path string, body map[string]any) (int, map[string]any, string) {
		t.Helper()
		rd := bytes.NewReader(nil)
		if body != nil {
			if _, has := body["request_id"]; !has && method == "POST" && !strings.HasSuffix(path, "/plan") {
				seq++
				body["request_id"] = fmt.Sprintf("data-wiring-request-%04d", seq)
				body["operator"], body["reason"] = "Pat Admin", "wiring test"
			}
			raw, _ := json.Marshal(body)
			rd = bytes.NewReader(raw)
		}
		req := httptest.NewRequest(method, path, rd)
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("X-Libre-Operator", "Pat Admin")
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		out := map[string]any{}
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		if strings.Contains(rec.Body.String(), token) {
			t.Errorf("%s %s returns the admin token", method, path)
		}
		return rec.Code, out, rec.Body.String()
	}
	store := func(calling string, ds *godicom.Dataset) (uint16, error) {
		scu, err := ae.Dial(ctx, ae.Config{AETitle: calling, PresentationContexts: []ae.PresentationContext{
			{AbstractSyntax: ctClass, TransferSyntaxes: []string{explicit}}}}, ln.Addr().String(), "WIRING")
		if err != nil {
			return 0, err
		}
		defer func() { _ = scu.Release(ctx) }()
		sop, _ := ds.GetString(godicom.MustTag("SOPInstanceUID"))
		res, err := scu.CStore(ctx, ae.StoreRequest{AffectedSOPClassUID: ctClass, AffectedSOPInstanceUID: sop, Data: ds})
		if err != nil {
			return 0, err
		}
		return res.Status, nil
	}

	// Two instances of patient W1, then one of the same study sent as W2:
	// that one is held as a patient conflict (default policy, hidden).
	for i, sop := range []string{wStudy + ".1.1", wStudy + ".1.2"} {
		if st, err := store("MODALITY", wiringImage("W1", "DOE^JANE", wStudy+".1", sop, i+1)); err != nil || st != status.Success {
			t.Fatalf("store = %04X %v", st, err)
		}
	}
	if st, err := store("MODALITY", wiringImage("W2", "ROE^RICHARD", wStudy+".1", wStudy+".1.3", 3)); err != nil || st != status.Success {
		t.Fatalf("store of the conflicting instance = %04X %v", st, err)
	}

	// ---- Study query ----
	code, body, raw := call("GET", "/control/v1", nil)
	if code != 200 || !strings.HasSuffix(fmt.Sprint(body["features"]), "backup studies send config-edit]") {
		t.Fatalf("discovery = %d %s", code, raw)
	}
	code, body, raw = call("GET", "/control/v1/studies?level=study&PatientName=doe*", nil)
	rows, _ := body["rows"].([]any)
	if code != 200 || len(rows) != 1 || body["held_instances"] != "hidden" {
		t.Fatalf("study query = %d %s", code, raw)
	}
	if r := rows[0].(map[string]any); r["PatientID"] != "W1" || r["StudyInstanceUID"] != wStudy || r["NumberOfStudyRelatedInstances"] != "2" || r["ModalitiesInStudy"] != "CT" {
		t.Fatalf("study row = %v", r)
	}
	for _, q := range []string{"level=study&PatientID=W2", "level=image&SOPInstanceUID=" + wStudy + ".1.3", "level=patient&PatientName=ROE*"} {
		if code, body, raw := call("GET", "/control/v1/studies?"+q, nil); code != 200 || len(body["rows"].([]any)) != 0 {
			t.Fatalf("the held instance is returned by %s: %d %s", q, code, raw)
		}
	}
	if code, body, raw = call("GET", "/control/v1/studies?level=image&StudyInstanceUID="+wStudy+"&limit=1", nil); code != 200 || body["more"] != true || body["cursor"] == nil {
		t.Fatalf("first page = %d %s", code, raw)
	}
	if code, body, raw = call("GET", "/control/v1/studies?level=image&StudyInstanceUID="+wStudy+"&limit=1&cursor="+fmt.Sprint(body["cursor"]), nil); code != 200 ||
		body["more"] != false || body["rows"].([]any)[0].(map[string]any)["SOPInstanceUID"] != wStudy+".1.2" {
		t.Fatalf("second page = %d %s", code, raw)
	}
	if code, body, raw = call("GET", "/control/v1/studies?level=study", nil); code != 422 || body["code"] != "no_matching_key" {
		t.Fatalf("query without a key = %d %s", code, raw)
	}
	code, body, _ = call("GET", "/control/v1/audit?type=Query", nil)
	if code != 200 || !strings.Contains(fmt.Sprint(body["records"]), "operator:Pat Admin") || !strings.Contains(fmt.Sprint(body["records"]), "PatientName=doe*") {
		t.Fatalf("no Query audit record with the operator: %v", body["records"])
	}

	// ---- Send ----
	sendReq := func() map[string]any {
		return map[string]any{"destination": "DEST", "patient_id": "W1", "study_instance_uid": wStudy}
	}
	code, body, raw = call("POST", "/control/v1/send/plan", sendReq())
	plan, _ := body["plan"].(map[string]any)
	if code != 200 || plan["instances"] != float64(2) || plan["patient_name"] != "DOE^JANE" ||
		plan["destination"].(map[string]any)["port"] != float64(destPort) || plan["destination"].(map[string]any)["ae_title"] != "DEST" {
		t.Fatalf("send plan = %d %s", code, raw)
	}
	digest := body["plan_digest"]
	for name, c := range map[string]struct {
		edit func(map[string]any)
		code string
	}{
		"no digest":      {func(m map[string]any) { m["confirm"] = true }, "plan_digest_required"},
		"not confirmed":  {func(m map[string]any) { m["plan_digest"] = digest }, "not_confirmed"},
		"wrong digest":   {func(m map[string]any) { m["confirm"], m["plan_digest"] = true, "sha256:00" }, "plan_changed"},
		"held patient":   {func(m map[string]any) { m["confirm"], m["plan_digest"], m["patient_id"] = true, digest, "W2" }, "patient_mismatch"},
		"no destination": {func(m map[string]any) { m["confirm"], m["plan_digest"], m["destination"] = true, digest, "ELSEWHERE" }, "unknown_destination"},
		"a held instance": {func(m map[string]any) {
			m["confirm"], m["plan_digest"], m["sop_instance_uids"] = true, digest, []string{wStudy + ".1.3"}
		}, "nothing_to_send"},
	} {
		m := sendReq()
		c.edit(m)
		if code, body, raw := call("POST", "/control/v1/send", m); code < 400 || body["code"] != c.code {
			t.Fatalf("send, %s = %d %s, want %s", name, code, raw, c.code)
		}
	}
	if r := received(); r != "" {
		t.Fatalf("refused sends delivered %s", r)
	}
	m := sendReq()
	m["confirm"], m["plan_digest"] = true, digest
	code, body, raw = call("POST", "/control/v1/send", m)
	if code != 200 || body["outcome"] != "complete" || body["completed"] != float64(2) || body["failed"] != float64(0) {
		t.Fatalf("send = %d %s", code, raw)
	}
	if r := received(); r != ".1.1=W1 .1.2=W1" {
		t.Fatalf("the destination received %q, want the two indexed instances of W1", r)
	}
	code, body, _ = call("GET", "/control/v1/audit?type=DICOMInstancesTransferred.sent", nil)
	if recs := fmt.Sprint(body["records"]); code != 200 || !strings.Contains(recs, "operator:Pat Admin") || !strings.Contains(recs, "destination:DEST") ||
		!strings.Contains(recs, wStudy) || !strings.Contains(recs, "completed:2") {
		t.Fatalf("send audit records = %v", body["records"])
	}

	// ---- Configuration edit ----
	edit := func() map[string]any {
		return map[string]any{"settings": map[string]any{
			"dicom.allowed_aes": []string{"MODALITY", "CT2@127.0.0.1"},
			"dicom.remote_aes": []map[string]any{{"name": "DEST", "host": "127.0.0.1", "port": destPort},
				{"name": "VIEWER2", "host": "viewer-2.example", "port": 11112, "ae_title": "V2"}}}}
	}
	// A setting that needs a restart is refused, whatever else is asked.
	for _, key := range []string{"dicom.tls_cert_file", "dicom.ae_title", "admin.token", "control.overlay_file", "dicom.patient_conflict_visibility"} {
		e := edit()
		e["settings"].(map[string]any)[key] = ""
		if code, body, raw := call("POST", "/control/v1/config/plan", e); code != 422 || body["code"] != "restart_required" {
			t.Fatalf("plan with %s = %d %s", key, code, raw)
		}
		e["confirm"], e["plan_digest"] = true, "sha256:x"
		if code, body, raw := call("POST", "/control/v1/config", e); code != 422 || body["code"] != "restart_required" {
			t.Fatalf("apply with %s = %d %s", key, code, raw)
		}
	}
	if licensing.Enforced() {
		// Unlicensed and not acknowledged: the edit is blocked like a reload.
		if code, body, raw := call("POST", "/control/v1/config/plan", edit()); code != http.StatusLocked || body["code"] != "license_ack_required" {
			t.Fatalf("plan on an unacknowledged node = %d %s, want 423", code, raw)
		}
		e := edit()
		e["confirm"], e["plan_digest"] = true, "sha256:x"
		if code, body, raw := call("POST", "/control/v1/config", e); code != http.StatusLocked || body["code"] != "license_ack_required" {
			t.Fatalf("apply on an unacknowledged node = %d %s, want 423", code, raw)
		}
		if _, err := os.Stat(overlay); !os.IsNotExist(err) {
			t.Fatal("a blocked edit wrote the overlay file")
		}
		if code, _, raw := call("POST", "/control/v1/license/ack", map[string]any{}); code != 200 {
			t.Fatalf("ack = %d %s", code, raw)
		}
	}
	code, body, raw = call("POST", "/control/v1/config/plan", edit())
	p, _ := body["plan"].(map[string]any)
	if code != 200 || p["changed"] != true || len(p["warnings"].([]any)) != 0 {
		t.Fatalf("config plan = %d %s", code, raw)
	}
	if _, err := os.Stat(overlay); !os.IsNotExist(err) {
		t.Fatal("a plan wrote the overlay file")
	}
	e := edit()
	e["confirm"], e["plan_digest"] = true, "sha256:not-this-plan"
	if code, body, raw = call("POST", "/control/v1/config", e); code != 409 || body["code"] != "plan_changed" {
		t.Fatalf("apply with another digest = %d %s", code, raw)
	}
	if rules, _ := a.gw.AllowedAEs(); fmt.Sprint(rules) != "[MODALITY]" {
		t.Fatalf("a refused edit changed the allow-list: %v", rules)
	}
	e = edit()
	e["confirm"], e["plan_digest"] = true, p["plan_digest"]
	code, body, raw = call("POST", "/control/v1/config", e)
	if code != 200 || body["applied"] != true || body["changed"] != true {
		t.Fatalf("apply = %d %s", code, raw)
	}
	if rules, _ := a.gw.AllowedAEs(); fmt.Sprint(rules) != "[MODALITY CT2@127.0.0.1]" || a.gw.RemoteAEs()["VIEWER2"].AETitle != "V2" || len(a.gw.RemoteAEs()) != 2 {
		t.Fatalf("in force after the edit: %v %v", rules, a.gw.RemoteAEs())
	}
	if b, _ := os.ReadFile(cfgPath); string(b) != cfgText {
		t.Fatal("config.yaml was rewritten")
	}
	if b, err := os.ReadFile(overlay); err != nil || !strings.Contains(string(b), "CT2@127.0.0.1") || !strings.Contains(string(b), "operator: Pat Admin") {
		t.Fatalf("overlay file: %v\n%s", err, b)
	}
	// The gateway enforces the new list on the wire: CT2 may now store.
	if st, err := store("CT2", wiringImage("W1", "DOE^JANE", wStudy+".1", wStudy+".1.4", 4)); err != nil || st != status.Success {
		t.Fatalf("store by the newly allowed AE = %04X %v", st, err)
	}
	if _, err := store("CT9", wiringImage("W1", "DOE^JANE", wStudy+".1", wStudy+".1.5", 5)); err == nil {
		t.Fatal("an AE that is not on the list was accepted")
	}
	// Emptying the list needs the acknowledged warning.
	empty := map[string]any{"settings": map[string]any{"dicom.allowed_aes": []string{}}}
	_, body, _ = call("POST", "/control/v1/config/plan", empty)
	empty["confirm"], empty["plan_digest"] = true, body["plan"].(map[string]any)["plan_digest"]
	if code, body, raw = call("POST", "/control/v1/config", empty); code != 422 || body["code"] != "warning_not_acknowledged" {
		t.Fatalf("emptying the allow-list without acknowledgement = %d %s", code, raw)
	}
	if _, enforced := a.gw.AllowedAEs(); !enforced {
		t.Fatal("the allow-list was emptied without the acknowledgement")
	}
	code, body, raw = call("GET", "/control/v1/config/history", nil)
	if code != 200 || body["current"].(map[string]any)["operator"] != "Pat Admin" || body["previous"].(map[string]any)["kind"] != "initial" {
		t.Fatalf("history = %d %s", code, raw)
	}
	code, body, _ = call("GET", "/control/v1/remote-aes", nil)
	if !strings.Contains(fmt.Sprint(body["remote_aes"]), "source:overlay") {
		t.Fatalf("remote AEs do not name the overlay as source: %v", body)
	}

	// ---- Restart: the overlay's settings are in force again ----
	stopGW()
	<-gwDone
	a.close()
	closed = true
	b, err := build(cfgPath)
	if err != nil {
		t.Fatalf("build after restart: %v", err)
	}
	defer b.close()
	if rules, _ := b.gw.AllowedAEs(); fmt.Sprint(rules) != "[MODALITY CT2@127.0.0.1]" || b.gw.RemoteAEs()["VIEWER2"].Host != "viewer-2.example" {
		t.Fatalf("after restart: %v %v", rules, b.gw.RemoteAEs())
	}
	h = b.rt.AdminHandler()
	if licensing.Enforced() {
		if code, _, raw := call("POST", "/control/v1/license/ack", map[string]any{}); code != 200 {
			t.Fatalf("ack after restart = %d %s", code, raw)
		}
	}
	// ---- Rollback: back to what config.yaml gives ----
	code, body, raw = call("POST", "/control/v1/config/rollback/plan", map[string]any{})
	if code != 200 {
		t.Fatalf("rollback plan = %d %s", code, raw)
	}
	rp := body["plan"].(map[string]any)
	rb := map[string]any{"confirm": true, "plan_digest": rp["plan_digest"]}
	if code, body, raw = call("POST", "/control/v1/config/rollback", rb); code != 422 || body["code"] != "warning_not_acknowledged" {
		t.Fatalf("rollback that removes a destination, not acknowledged = %d %s", code, raw)
	}
	rb = map[string]any{"confirm": true, "plan_digest": rp["plan_digest"], "acknowledge_warnings": []string{"remote_ae_removed", "allow_list_entry_removed"}}
	if code, body, raw = call("POST", "/control/v1/config/rollback", rb); code != 200 || body["applied"] != true {
		t.Fatalf("rollback = %d %s", code, raw)
	}
	if rules, _ := b.gw.AllowedAEs(); fmt.Sprint(rules) != "[MODALITY]" || len(b.gw.RemoteAEs()) != 1 {
		t.Fatalf("after rollback: %v %v", rules, b.gw.RemoteAEs())
	}
	if bts, _ := os.ReadFile(cfgPath); string(bts) != cfgText {
		t.Fatal("config.yaml was rewritten")
	}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
