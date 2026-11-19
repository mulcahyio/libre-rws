package engine

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dvt/internal/result"
)

// jsonWorker answers every call with out (or the shell body when out
// starts with "#!").
func jsonWorker(t *testing.T, out string) (Config, string) {
	t.Helper()
	dir := t.TempDir()
	body := out
	if !strings.HasPrefix(out, "#!") {
		if err := os.WriteFile(filepath.Join(dir, "out.json"), []byte(out), 0o600); err != nil {
			t.Fatal(err)
		}
		body = `d=$(dirname "$0"); printf '%s\n' "$@" > "$d/argv"; cat "$d/out.json"`
	} else {
		body = strings.TrimPrefix(out, "#!")
	}
	return Config{Python: "/bin/sh", Worker: script(t, dir, "worker.sh", body), Timeout: 5 * time.Second, Version: "0.2.0"}, dir
}

const okAssociate = `{"exchanges":[
 {"seq":1,"protocol":"dimse","operation":"A-ASSOCIATE","endpoint":"h:104","started":"2026-11-19T00:00:00.000Z","duration_ms":3,"outcome":"completed",
  "association":{"result":"accepted","released":true,"max_pdu":16384,"impl_class_uid":"1.2.3","impl_version":"V1",
   "contexts":[{"id":1,"abstract_syntax":"1.2.840.10008.1.1","proposed_transfer_syntaxes":["1.2.840.10008.1.2"],"result":"accepted","accepted_transfer_syntax":"1.2.840.10008.1.2"},
               {"id":3,"abstract_syntax":"1.2.3.4","proposed_transfer_syntaxes":["1.2.840.10008.1.2.1","1.2.840.10008.1.2"],"result":"rejected","reason":"abstract-syntax-not-supported (3)"}]}},
 {"seq":2,"protocol":"dimse","operation":"A-RELEASE","endpoint":"h:104","started":"2026-11-19T00:00:00.010Z","duration_ms":1,"outcome":"completed"}]}`

func assocReq() AssociateRequest {
	return AssociateRequest{Contexts: []AssocContext{
		{AbstractSyntax: "1.2.840.10008.1.1", TransferSyntaxes: []string{"1.2.840.10008.1.2"}},
		{AbstractSyntax: "1.2.3.4", TransferSyntaxes: []string{"1.2.840.10008.1.2.1", "1.2.840.10008.1.2"}},
	}}
}

var testPeer = Peer{Host: "h", Port: 104, CalledAET: "A", CallingAET: "B", Timeout: time.Second}

// Verifies: DVT-SRS-090, DVT-SRS-150
func TestAssociateOutputSchemaFailsClosed(t *testing.T) {
	c, dir := jsonWorker(t, okAssociate)
	req := assocReq()
	r, _, err := c.Associate(context.Background(), testPeer, req)
	if err != nil || len(r.Exchanges) != 2 || r.Exchanges[0].Association.Contexts[1].Result != "rejected" {
		t.Fatalf("valid output refused: %v %+v", err, r)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	for _, want := range []string{"associate", "--context\n1.2.840.10008.1.1;1.2.840.10008.1.2\n", "--context\n1.2.3.4;1.2.840.10008.1.2.1,1.2.840.10008.1.2\n", "--impl-uid"} {
		if !strings.Contains(string(argv), want) {
			t.Fatalf("argv lacks %q:\n%s", want, argv)
		}
	}
	if strings.Contains(string(argv), "--tls") {
		t.Fatal("TLS options for a target without TLS")
	}
	bad := map[string]string{
		"garbage":            "not json",
		"two documents":      okAssociate + okAssociate,
		"unknown field":      strings.Replace(okAssociate, `"released":true`, `"released":true,"passed":true`, 1),
		"no exchange":        `{"exchanges":[]}`,
		"bad seq":            strings.Replace(okAssociate, `"seq":2`, `"seq":5`, 1),
		"unknown operation":  strings.Replace(okAssociate, `"A-RELEASE"`, `"C-ECHO"`, 1),
		"wrong protocol":     strings.Replace(okAssociate, `"protocol":"dimse","operation":"A-ASSOCIATE"`, `"protocol":"http","operation":"A-ASSOCIATE"`, 1),
		"unknown outcome":    strings.Replace(okAssociate, `"duration_ms":3,"outcome":"completed"`, `"duration_ms":3,"outcome":"fine"`, 1),
		"unknown result":     strings.Replace(okAssociate, `"result":"accepted","released"`, `"result":"ok","released"`, 1),
		"context missing":    regexp.MustCompile(`,\s*\{"id":3[^}]*\}`).ReplaceAllString(okAssociate, ""),
		"context renumbered": strings.Replace(okAssociate, `"id":3`, `"id":5`, 1),
		"context other uid":  strings.Replace(okAssociate, `"abstract_syntax":"1.2.3.4"`, `"abstract_syntax":"1.2.3.5"`, 1),
		"context result":     strings.Replace(okAssociate, `"result":"rejected","reason"`, `"result":"maybe","reason"`, 1),
		"error kind":         strings.Replace(okAssociate, `"duration_ms":1,"outcome":"completed"`, `"duration_ms":1,"outcome":"error","error":{"kind":"oops","message":"x"}`, 1),
		"error without kind": strings.Replace(okAssociate, `"duration_ms":1,"outcome":"completed"`, `"duration_ms":1,"outcome":"error"`, 1),
		"TLS on plain":       tlsFirst(okAssociate),
		"released no RP": strings.Replace(okAssociate, `,
 {"seq":2,"protocol":"dimse","operation":"A-RELEASE","endpoint":"h:104","started":"2026-11-19T00:00:00.010Z","duration_ms":1,"outcome":"completed"}`, ``, 1),
		"accepted in reject": strings.Replace(okAssociate, `"result":"accepted","released":true`, `"result":"rejected","reject":{"result":1,"source":1,"reason":3},"released":false`, 1),
		"crash":              "#!echo boom >&2; exit 3",
		"hang":               "#!sleep 30",
	}
	for name, out := range bad {
		c, _ := jsonWorker(t, out)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _, err := c.Associate(ctx, testPeer, req)
		cancel()
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	// Missing worker.
	c.Python = filepath.Join(dir, "no-python")
	if _, _, err := c.Associate(context.Background(), testPeer, req); err == nil {
		t.Error("missing worker: accepted")
	}
}

// Verifies: DVT-SRS-100, DVT-SRS-104, DVT-SRS-153
func TestAssociateTLSArgumentsArePathsOnly(t *testing.T) {
	c, dir := jsonWorker(t, tlsFirst(okAssociate))
	p := testPeer
	p.TLS = &TLS{MinVersion: "1.3", CAFile: "/run/secrets/ca.pem", ServerName: "pacs.lab", ClientCert: "/run/secrets/c.pem", ClientKey: "/run/secrets/c.key"}
	req := assocReq()
	if _, _, err := c.Associate(context.Background(), p, req); err != nil {
		t.Fatal(err)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	for _, want := range []string{"--tls\n", "--tls-min\n1.3\n", "--ca-file\n/run/secrets/ca.pem\n", "--server-name\npacs.lab\n", "--client-cert\n/run/secrets/c.pem\n", "--client-key\n/run/secrets/c.key\n"} {
		if !strings.Contains(string(argv), want) {
			t.Fatalf("argv lacks %q:\n%s", want, argv)
		}
	}
	// A TLS target whose output has no handshake is a schema error.
	c2, _ := jsonWorker(t, okAssociate)
	if _, _, err := c2.Associate(context.Background(), p, req); err == nil || !strings.Contains(err.Error(), "no TLS handshake") {
		t.Fatalf("TLS target without handshake: %v", err)
	}
	// Roles and the asynchronous operations window reach the worker.
	r2 := assocReq()
	r2.Contexts[1].Roles = &result.Roles{SCU: false, SCP: true}
	r2.AsyncOps = &result.AsyncOps{Invoked: 2, Performed: 3}
	c3, dir3 := jsonWorker(t, okAssociate)
	if _, _, err := c3.Associate(context.Background(), testPeer, r2); err != nil {
		t.Fatal(err)
	}
	argv, _ = os.ReadFile(filepath.Join(dir3, "argv"))
	if !strings.Contains(string(argv), "1.2.3.4;1.2.840.10008.1.2.1,1.2.840.10008.1.2;01\n") || !strings.Contains(string(argv), "--async-ops\n2,3\n") {
		t.Fatalf("roles or async ops not passed:\n%s", argv)
	}
}

// tlsFirst puts a completed TLS handshake in front of the exchanges.
func tlsFirst(out string) string {
	out = strings.Replace(out, `"seq":2,"protocol":"dimse","operation":"A-RELEASE"`, `"seq":3,"protocol":"dimse","operation":"A-RELEASE"`, 1)
	return strings.Replace(out, `{"seq":1,"protocol":"dimse"`, `{"seq":1,"protocol":"tls","operation":"TLS-HANDSHAKE","endpoint":"h:104","started":"2026-11-19T00:00:00.000Z","duration_ms":1,"outcome":"completed","tls":{"version":"TLSv1.3","cipher":"x","verified":true,"client_cert_sent":true}},{"seq":2,"protocol":"dimse"`, 1)
}

const okProbe = `{"mode":"plaintext","observation":"connection_closed","detail":"the target closed the connection without answering","exchanges":[
 {"seq":1,"protocol":"dimse","operation":"A-ASSOCIATE","endpoint":"h:104","started":"2026-11-19T00:00:00.000Z","duration_ms":3,"outcome":"refused","association":{"result":"no_response","released":false}}]}`

// Verifies: DVT-SRS-105, DVT-SRS-106, DVT-SRS-150
func TestTLSProbeOutputSchemaAndNoCredentials(t *testing.T) {
	c, dir := jsonWorker(t, okProbe)
	p := testPeer
	p.TLS = &TLS{CAFile: "/run/secrets/ca.pem", ClientCert: "/run/secrets/c.pem", ClientKey: "/run/secrets/c.key"}
	r, _, err := c.TLSProbe(context.Background(), p, "plaintext", "")
	if err != nil || r.Observation != ObsConnectionClosed {
		t.Fatalf("valid probe output refused: %v %+v", err, r)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	for _, banned := range []string{"--client-cert", "--client-key", "c.key", "--tls\n"} {
		if strings.Contains(string(argv), banned) {
			t.Fatalf("probe argv holds %q:\n%s", banned, argv)
		}
	}
	if !strings.Contains(string(argv), "--mode\nplaintext\n") || !strings.Contains(string(argv), "--ca-file\n/run/secrets/ca.pem\n") {
		t.Fatalf("probe argv:\n%s", argv)
	}
	c2, dir2 := jsonWorker(t, strings.Replace(okProbe, `"mode":"plaintext"`, `"mode":"tls_version"`, 1))
	if _, _, err := c2.TLSProbe(context.Background(), p, "tls_version", "1.1"); err != nil {
		t.Fatal(err)
	}
	argv, _ = os.ReadFile(filepath.Join(dir2, "argv"))
	if !strings.Contains(string(argv), "--tls-version\n1.1\n") {
		t.Fatalf("tls version not passed:\n%s", argv)
	}
	bad := map[string]string{
		"garbage":          "{",
		"unknown field":    strings.Replace(okProbe, `"mode":"plaintext"`, `"mode":"plaintext","refused":true`, 1),
		"other mode":       strings.Replace(okProbe, `"mode":"plaintext"`, `"mode":"no_client_cert"`, 1),
		"unknown obs":      strings.Replace(okProbe, `"connection_closed"`, `"refused"`, 1),
		"no detail":        strings.Replace(okProbe, `"detail":"the target closed the connection without answering"`, `"detail":""`, 1),
		"release":          strings.Replace(okProbe, `"operation":"A-ASSOCIATE"`, `"operation":"A-RELEASE"`, 1),
		"client cert sent": strings.Replace(okProbe, `{"seq":1,"protocol":"dimse"`, `{"seq":1,"protocol":"tls","operation":"TLS-HANDSHAKE","endpoint":"h:104","started":"2026-11-19T00:00:00.000Z","duration_ms":1,"outcome":"completed","tls":{"version":"TLSv1.2","cipher":"x","verified":true,"client_cert_sent":true}},{"seq":2,"protocol":"dimse"`, 1),
		"tls without info": strings.Replace(okProbe, `"protocol":"dimse","operation":"A-ASSOCIATE"`, `"protocol":"tls","operation":"TLS-HANDSHAKE"`, 1),
		"crash":            "#!exit 1",
		"hang":             "#!sleep 30",
	}
	for name, out := range bad {
		c, _ := jsonWorker(t, out)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_, _, err := c.TLSProbe(ctx, p, "plaintext", "")
		cancel()
		if err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}
