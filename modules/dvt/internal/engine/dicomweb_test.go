package engine

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

var testWeb = Web{BaseURL: "http://127.0.0.1:8042/dicom-web", Timeout: time.Second,
	Limits: Limits{MaxResponseBytes: 1 << 20, MaxParts: 10, MaxPartBytes: 1 << 20, MaxJSONBytes: 1 << 16, MaxPages: 3, MaxBodyExcerptBytes: 100}}

const xGet = `{"seq":1,"protocol":"http","operation":"GET","endpoint":"http://127.0.0.1:8042/dicom-web/studies","started":"2026-11-19T00:00:00.000Z","duration_ms":3,"outcome":"completed",
 "http":{"method":"GET","url":"http://127.0.0.1:8042/dicom-web/studies","request_headers":{"Authorization":["[REDACTED]"]},"status":200,"body":{"content_type":"application/dicom+json","size":2,"sha256":"x"}}}`

const okQido = `{"exchanges":[` + xGet + `],"keywords":{"PatientID":"00100020"},
 "query":[{"key":"PatientID","tag":"00100020","vr":"LO","value":"P1","exact":true}],
 "pages":[{"search":"","offset":null,"exchange":1,"status":200,"content_type":"application/dicom+json","json":"array","detail":"","objects":[{"present":["0020000D"],"values":{"0020000D":["1.2.3"]}}]}],
 "page_limit_reached":false}`

func qidoReq() QidoRequest {
	return QidoRequest{Level: "studies", Query: [][2]string{{"PatientID", "P1"}}, Keywords: []string{"PatientID"}}
}

// Verifies: DVT-SRS-113, DVT-SRS-118, DVT-SRS-150, DVT-SRS-153
func TestQidoOutputSchemaFailsClosed(t *testing.T) {
	w := testWeb
	w.TokenFile = "/run/secrets/token"
	c, dir := jsonWorker(t, okQido)
	if _, _, err := c.Qido(context.Background(), w, qidoReq()); err != nil {
		t.Fatalf("valid output refused: %v", err)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	for _, want := range []string{"qido\n", "--base-url\nhttp://127.0.0.1:8042/dicom-web\n", "--token-file\n/run/secrets/token\n",
		"--query\nPatientID=P1\n", "--keyword\nPatientID\n", "--max-pages\n3\n", "--max-excerpt\n100\n"} {
		if !strings.Contains(string(argv), want) {
			t.Fatalf("argv lacks %q:\n%s", want, argv)
		}
	}
	if strings.Contains(string(argv), "--tls-min") {
		t.Fatal("TLS options for an http base URL")
	}
	bad := map[string]string{
		"garbage":             "not json",
		"unknown field":       strings.Replace(okQido, `"page_limit_reached":false`, `"page_limit_reached":false,"passed":true`, 1),
		"no search":           strings.Replace(strings.Replace(okQido, `"exchanges":[`+xGet+`]`, `"exchanges":[]`, 1), `"pages":[{`, `"pages_x":[{`, 1),
		"page without exch":   strings.Replace(okQido, `"exchanges":[`+xGet+`]`, `"exchanges":[]`, 1),
		"unknown json kind":   strings.Replace(okQido, `"json":"array"`, `"json":"fine"`, 1),
		"objects without arr": strings.Replace(okQido, `"json":"array"`, `"json":"invalid"`, 1),
		"status mismatch":     strings.Replace(okQido, `"status":200,"content_type"`, `"status":204,"content_type"`, 1),
		"auth not redacted":   strings.Replace(okQido, `["[REDACTED]"]`, `["Bearer abc"]`, 1),
		"query not resolved":  strings.Replace(okQido, `"tag":"00100020"`, `"tag":"PatientID"`, 1),
		"other query key":     strings.Replace(okQido, `"key":"PatientID"`, `"key":"PatientName"`, 1),
		"keyword missing":     strings.Replace(okQido, `"keywords":{"PatientID":"00100020"}`, `"keywords":{}`, 1),
		"wrong protocol":      strings.Replace(okQido, `"protocol":"http"`, `"protocol":"dimse"`, 1),
		"no http details":     strings.Replace(okQido, `"outcome":"completed",`+"\n"+` "http":{`, `"outcome":"completed","x_http":{`, 1),
		"error kind":          strings.Replace(okQido, `"outcome":"completed"`, `"outcome":"error","error":{"kind":"oops","message":"x"}`, 1),
		"tls on http":         strings.Replace(okQido, `"outcome":"completed",`, `"outcome":"completed","tls":{"version":"TLSv1.3","cipher":"x","verified":true,"client_cert_sent":false},`, 1),
		"config then request": strings.Replace(okQido, `"page_limit_reached":false`, `"page_limit_reached":false,"config_error":"bad"`, 1),
	}
	for name, out := range bad {
		c, _ := jsonWorker(t, out)
		if _, _, err := c.Qido(context.Background(), w, qidoReq()); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

const xGetWado = `{"seq":1,"protocol":"http","operation":"GET","endpoint":"http://127.0.0.1:8042/dicom-web/studies/1.2","started":"2026-11-19T00:00:00.000Z","duration_ms":3,"outcome":"completed",
 "http":{"method":"GET","url":"http://127.0.0.1:8042/dicom-web/studies/1.2","status":200,"body":{"content_type":"multipart/related","size":2,"sha256":"x"},
 "parts":[{"index":1,"content_type":"application/dicom","transfer_syntax":"1.2.840.10008.1.2.1","sop_instance_uid":"1.2.3","size":10,"sha256":"y"}]}}`

const okWado = `{"exchanges":[` + xGetWado + `],"parts":[{"exchange":1,"index":1,"content_type":"application/dicom","media_type":"application/dicom","ts_param":"","size":10,"sha256":"y","error":"","transfer_syntax":"1.2.840.10008.1.2.1","sop_instance_uid":"1.2.3","sop_class_uid":"1.2.840.10008.5.1.4.1.1.2","study_instance_uid":"1.2","series_instance_uid":"1.2.1","file":"/w/p1.dcm"}],
 "metadata":[],"originals":[],"keywords":{}}`

// Verifies: DVT-SRS-119, DVT-SRS-126, DVT-SRS-150
func TestWadoOutputSchemaFailsClosed(t *testing.T) {
	req := WadoRequest{Resources: []string{"/studies/1.2"}, TransferSyntax: "1.2.840.10008.1.2.1", OutDir: "/w"}
	c, dir := jsonWorker(t, okWado)
	if _, _, err := c.Wado(context.Background(), testWeb, req); err != nil {
		t.Fatalf("valid output refused: %v", err)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	for _, want := range []string{"wado\n", "--resource\n/studies/1.2\n", "--transfer-syntax\n1.2.840.10008.1.2.1\n", "--out\n/w\n"} {
		if !strings.Contains(string(argv), want) {
			t.Fatalf("argv lacks %q:\n%s", want, argv)
		}
	}
	bad := map[string]string{
		"unknown field":      strings.Replace(okWado, `"keywords":{}`, `"keywords":{},"ok":true`, 1),
		"other resource":     strings.Replace(okWado, `dicom-web/studies/1.2","started"`, `dicom-web/studies/9.9","started"`, 1),
		"part bad exchange":  strings.Replace(okWado, `"parts":[{"exchange":1`, `"parts":[{"exchange":7`, 1),
		"part without facts": strings.Replace(strings.Replace(okWado, `"sop_instance_uid":"1.2.3","sop_class_uid"`, `"sop_instance_uid":"","sop_class_uid"`, 1), `"file":"/w/p1.dcm"`, `"file":""`, 1),
		"metadata records":   strings.Replace(okWado, `"metadata":[]`, `"metadata":[{"exchange":1,"study_instance_uid":"","series_instance_uid":"","sop_instance_uid":"","attrs":{}}]`, 1),
		"no exchange":        strings.Replace(okWado, `"exchanges":[`+xGetWado+`]`, `"exchanges":[]`, 1),
	}
	for name, out := range bad {
		c, _ := jsonWorker(t, out)
		if _, _, err := c.Wado(context.Background(), testWeb, req); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

const xPost = `{"seq":1,"protocol":"http","operation":"POST","endpoint":"http://127.0.0.1:8042/dicom-web/studies","started":"2026-11-19T00:00:00.000Z","duration_ms":3,"outcome":"completed",
 "http":{"method":"POST","url":"http://127.0.0.1:8042/dicom-web/studies","status":200,"body":{"content_type":"application/dicom+json","size":2,"sha256":"x"}}}`

const okStow = `{"exchanges":[` + xPost + `],
 "files":[{"file":"/f/a.dcm","error":"","sop_instance_uid":"1.2.3","sop_class_uid":"1.2.840.10008.5.1.4.1.1.2","study_instance_uid":"1.2","series_instance_uid":"1.2.1","transfer_syntax":"1.2.840.10008.1.2.1","size":10,"sha256":"y"}],
 "requests":[{"files":[0],"exchange":1,"status":200,"json":"object","detail":"","referenced":[{"sop_class_uid":"1.2.840.10008.5.1.4.1.1.2","sop_instance_uid":"1.2.3","retrieve_url":"","warning_reason":""}],"failed":[],"retrieve_url":"","warning_reason":""}]}`

// Verifies: DVT-SRS-128, DVT-SRS-133, DVT-SRS-136, DVT-SRS-150
func TestStowOutputSchemaFailsClosed(t *testing.T) {
	files := []string{"/f/a.dcm"}
	c, dir := jsonWorker(t, okStow)
	if _, _, err := c.Stow(context.Background(), testWeb, "", 10, files); err != nil {
		t.Fatalf("valid output refused: %v", err)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	if !strings.Contains(string(argv), "stow\n") || !strings.Contains(string(argv), "--batch-size\n10\n") || !strings.HasSuffix(string(argv), "/f/a.dcm\n") {
		t.Fatalf("argv:\n%s", argv)
	}
	bad := map[string]string{
		"unknown field":      strings.Replace(okStow, `"warning_reason":""}]}`, `"warning_reason":"","stored":true}]}`, 1),
		"other file":         strings.Replace(okStow, `"file":"/f/a.dcm"`, `"file":"/f/b.dcm"`, 1),
		"file incomplete":    strings.Replace(okStow, `"sha256":"y"`, `"sha256":""`, 1),
		"sent despite error": strings.Replace(okStow, `"file":"/f/a.dcm","error":""`, `"file":"/f/a.dcm","error":"not DICOM"`, 1),
		"no request":         strings.Replace(okStow, `"requests":[{`, `"requests_x":[{`, 1),
		"json kind":          strings.Replace(okStow, `"json":"object"`, `"json":"maybe"`, 1),
		"status mismatch":    strings.Replace(okStow, `"exchange":1,"status":200`, `"exchange":1,"status":202`, 1),
		"get instead":        strings.Replace(strings.Replace(okStow, `"operation":"POST"`, `"operation":"GET"`, 1), `"method":"POST"`, `"method":"GET"`, 1),
	}
	for name, out := range bad {
		c, _ := jsonWorker(t, out)
		if _, _, err := c.Stow(context.Background(), testWeb, "", 10, files); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// Verifies: DVT-SRS-150
func TestDICOMwebWorkerFailuresAreEngineErrors(t *testing.T) {
	short := testWeb
	short.Timeout = 100 * time.Millisecond
	cases := map[string]string{
		"crash":   "#!echo boom >&2; exit 3",
		"garbage": "#!echo '{not json'",
		"empty":   "#!true",
		"schema":  `{"exchanges":"x"}`,
	}
	for name, body := range cases {
		c, _ := jsonWorker(t, body)
		if _, _, err := c.Qido(context.Background(), short, qidoReq()); err == nil {
			t.Errorf("qido %s: no error", name)
		}
		if _, _, err := c.Wado(context.Background(), short, WadoRequest{Resources: []string{"/studies/1"}, OutDir: "/w"}); err == nil {
			t.Errorf("wado %s: no error", name)
		}
		if _, _, err := c.Stow(context.Background(), short, "", 1, []string{"/f/a.dcm"}); err == nil {
			t.Errorf("stow %s: no error", name)
		}
		if _, _, err := c.TLSProbeWeb(context.Background(), short, "plaintext", ""); err == nil {
			t.Errorf("tls-probe %s: no error", name)
		}
	}
	// Missing worker.
	c := Config{Python: "/nonexistent/python3", Worker: "/nonexistent/w.py", Timeout: time.Second}
	if _, _, err := c.Qido(context.Background(), short, qidoReq()); err == nil {
		t.Error("missing worker: no error")
	}
	// Hang: the process is killed at its timeout (2 ops + 15 s headroom
	// would be slow, so the context bounds it here).
	c, _ = jsonWorker(t, "#!sleep 30")
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	start := time.Now()
	if _, _, err := c.Stow(ctx, short, "", 1, []string{"/f/a.dcm"}); err == nil || time.Since(start) > 10*time.Second {
		t.Errorf("hang: err %v after %s", err, time.Since(start))
	}
}

// Verifies: DVT-SRS-105, DVT-SRS-106
func TestTLSProbeWebSchema(t *testing.T) {
	ok := `{"mode":"plaintext","observation":"http_reply","detail":"HTTP 400","exchanges":[
 {"seq":1,"protocol":"http","operation":"GET","endpoint":"https://h/dw/studies?limit=1","started":"2026-11-19T00:00:00.000Z","duration_ms":1,"outcome":"completed","http":{"method":"GET","url":"https://h/dw/studies?limit=1","status":400}}]}`
	w := Web{BaseURL: "https://h/dw", Timeout: time.Second, TokenFile: "/run/secrets/t", TLS: &TLS{CAFile: "/run/secrets/ca.pem", ClientCert: "/c", ClientKey: "/k"}}
	c, dir := jsonWorker(t, ok)
	r, _, err := c.TLSProbeWeb(context.Background(), w, "plaintext", "")
	if err != nil || ProbeHTTPStatus(r) != 400 {
		t.Fatalf("valid probe refused: %v %+v", err, r)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	for _, no := range []string{"--token-file", "/run/secrets/t", "--client-cert", "--client-key"} {
		if strings.Contains(string(argv), no) {
			t.Fatalf("probe got %s:\n%s", no, argv)
		}
	}
	bad := map[string]string{
		"headers sent":   strings.Replace(ok, `"status":400`, `"status":400,"request_headers":{"Authorization":["[REDACTED]"]}`, 1),
		"client cert":    strings.Replace(ok, `"outcome":"completed",`, `"outcome":"completed","tls":{"version":"","cipher":"","verified":false,"client_cert_sent":true},`, 1),
		"no status":      strings.Replace(ok, `"status":400`, `"status":0`, 1),
		"unknown obs":    strings.Replace(ok, `"http_reply"`, `"fine"`, 1),
		"other mode":     strings.Replace(ok, `"plaintext"`, `"tls_version"`, 1),
		"wrong protocol": strings.Replace(ok, `"protocol":"http"`, `"protocol":"dimse"`, 1),
	}
	for name, out := range bad {
		c, _ := jsonWorker(t, out)
		if _, _, err := c.TLSProbeWeb(context.Background(), w, "plaintext", ""); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
