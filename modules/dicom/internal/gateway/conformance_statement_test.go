package gateway

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/godicom-dev/godicom"
	"github.com/godicom-dev/godicom/uid"
	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
	"github.com/godicom-dev/gonetdicom/status"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/dcm"
	"github.com/mulcahyio/libre-rws/modules/dicom/internal/index"
	"github.com/mulcahyio/libre-rws/platform/dicomuid"
)

// The DICOM Conformance Statement (docs/dhf/libredcm/dicom-conformance-statement.md)
// carries tables between "<!-- dcs:begin NAME -->" and "<!-- dcs:end NAME -->"
// markers. This test renders each table from the running code and fails when
// the document differs, so the statement cannot drift from what LibreDCM
// negotiates and answers. To regenerate the tables after a code change:
//
//	rws-go test ./modules/dicom/internal/gateway -run TestConformanceStatementMatchesCode -update-dcs
//
// (or LIBRE_DCM_UPDATE_DCS=1 with a host Go toolchain).
//
// scripts/qms/dcs.py checks the same tables independently from the Go source
// text (no shared code), in the qms workflow.
const dcsPath = "../../../../docs/dhf/libredcm/dicom-conformance-statement.md"

var updateDCS = flag.Bool("update-dcs", false, "rewrite the generated tables of the DICOM Conformance Statement")

var dcsBlockRe = regexp.MustCompile(`(?s)(<!-- dcs:begin ([a-z0-9-]+) -->\n)(.*?)(<!-- dcs:end ([a-z0-9-]+) -->)`)

// dcsTable renders a Markdown table.
func dcsTable(header []string, rows [][]string) string {
	var b strings.Builder
	b.WriteString("| " + strings.Join(header, " | ") + " |\n")
	seps := make([]string, len(header))
	for i := range seps {
		seps[i] = "---"
	}
	b.WriteString("|" + strings.Join(seps, "|") + "|\n")
	for _, r := range rows {
		b.WriteString("| " + strings.Join(r, " | ") + " |\n")
	}
	return b.String()
}

func uidName(t *testing.T, u string) (name, keyword string) {
	t.Helper()
	info, ok := uid.Lookup(uid.UID(u))
	if !ok {
		t.Fatalf("UID %s is not in the DICOM dictionary", u)
	}
	return info.Name, info.Keyword
}

// uidLess orders UIDs numerically, component by component (PS3.2 Table N.1-1).
func uidLess(a, b string) bool {
	pa, pb := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(pa) && i < len(pb); i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		if x != y {
			return x < y
		}
	}
	return len(pa) < len(pb)
}

// tsSet classifies a storage transfer syntax: U (uncompressed), LL
// (lossless compressed) or L (lossy compressed), from its PS3.6 name.
func tsSet(t *testing.T, ts string) string {
	if dcm.IsUncompressed(ts) {
		return "U"
	}
	name, _ := uidName(t, ts)
	if strings.Contains(name, "Lossless") && !strings.Contains(name, "Lossy") {
		return "LL"
	}
	return "L"
}

var tsSetOrder = map[string]int{"U": 0, "LL": 1, "L": 2}

// matchingType renders how a key matches at a level, in the vocabulary of
// PS3.2 Table N.5-18. UNIVERSAL means the key's value is returned.
func matchingType(kind matchKind, matches, returned bool) string {
	var m []string
	if matches {
		m = append(m, "SINGLE_VALUE")
		switch kind {
		case kString, kPersonName:
			m = append(m, "WILDCARD")
		case kUID:
			m = append(m, "UID")
		case kDate, kTime:
			m = append(m, "RANGE")
		}
	}
	if returned {
		m = append(m, "UNIVERSAL")
	}
	return strings.Join(m, "; ")
}

func keyComment(kind matchKind, matches, returned bool) string {
	var c []string
	switch {
	case kind == kReturn:
		c = append(c, "Return key, computed by LibreDCM")
	case kind == kPersonName:
		c = append(c, "Case-insensitive unless `find_case_sensitive_names` is set")
	case kind == kModality:
		c = append(c, "Matches a study with at least one series of any listed modality")
	case kind == kString:
		c = append(c, "Case-sensitive")
	case kind == kTime:
		c = append(c, "A partial time covers its whole period")
	}
	if matches && !returned {
		c = append(c, "matching only: returned zero-length at this level")
	}
	return strings.Join(c, "; ")
}

// returnedKeys stores one instance with every indexed attribute set and asks
// C-FIND for all query keys at each level: a key is "returned" when the
// response carries a value for it.
func returnedKeys(t *testing.T) map[index.Level]map[string]bool {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	addr, gw, _ := startGateway(t, ctx, nil)
	study, series := "1.3.6.1.4.1.67686.9.1.30", "1.3.6.1.4.1.67686.9.1.30.1"
	ds := ctImage(study, series, series+".1", 1)
	for kw, v := range map[string]string{
		"PatientBirthDate": "19700101", "PatientSex": "O", "StudyTime": "101500", "StudyID": "S1",
		"StudyDescription": "DCS", "ReferringPhysicianName": "REF^DOC", "SeriesDescription": "AX",
	} {
		_ = ds.SetString(godicom.MustTag(kw), v)
	}
	scu, err := dialAs(ctx, addr, "MODALITY")
	if err != nil {
		t.Fatal(err)
	}
	res, err := scu.CStore(ctx, ae.StoreRequest{AffectedSOPClassUID: string(uid.CTImageStorage), AffectedSOPInstanceUID: series + ".1", Data: ds})
	if err != nil || res.Status != status.Success {
		t.Fatalf("store: %v %v", err, res)
	}
	_ = scu.Release(ctx)

	out := map[index.Level]map[string]bool{}
	for _, lvl := range []index.Level{index.Patient, index.Study, index.Series, index.Image} {
		q := godicom.NewDataset()
		_ = q.SetString(godicom.MustTag("QueryRetrieveLevel"), string(lvl))
		for kw := range queryKeys {
			q.Set(godicom.NewDataElement(godicom.MustTag(kw), godicom.LookupVR(godicom.MustTag(kw)), nil))
		}
		model := ae.StudyRootQueryRetrieveInformationModelFind
		if lvl == index.Patient {
			model = ae.PatientRootQueryRetrieveInformationModelFind
		}
		ms := gw.onCFind(ctx, ae.FindRequest{QueryModel: model, IdentifierData: q})
		if len(ms) != 2 || ms[0].Status != status.Pending || ms[1].Status != status.Success {
			t.Fatalf("%s: C-FIND answered %d responses, want one match and success", lvl, len(ms))
		}
		out[lvl] = map[string]bool{}
		for kw := range queryKeys {
			if dcm.String(ms[0].Identifier, kw) != "" {
				out[lvl][kw] = true
			}
		}
	}
	return out
}

func tagString(kw string) string {
	tg := godicom.MustTag(kw)
	return fmt.Sprintf("(%04X,%04X)", tg.Group(), tg.Element())
}

func attrName(t *testing.T, kw string) string {
	t.Helper()
	e, ok := godicom.Standard().Lookup(godicom.MustTag(kw), "")
	if !ok {
		t.Fatalf("no dictionary entry for %s", kw)
	}
	return e.Name
}

// dcsExpected renders every generated table of the conformance statement.
func dcsExpected(t *testing.T) map[string]string {
	t.Helper()
	g := &Gateway{cfg: DefaultConfig()}
	sc := g.serverConfig()
	storage := map[string]bool{}
	for _, u := range ae.AllStorageSOPClasses {
		storage[u] = true
	}
	blocks := map[string]string{}

	// Verification and Query/Retrieve SOP classes (non-storage).
	rows := [][]string{}
	name, kw := uidName(t, pdu.VerificationSOPClass)
	// Verification SCU: `libredcm -echo` and POST /control/v1/echo.
	rows = append(rows, []string{name, "`" + kw + "`", "`" + pdu.VerificationSOPClass + "`", "Y", "Y"})
	for _, u := range sc.AcceptedAbstractSyntaxes {
		if storage[u] || u == "*" {
			continue
		}
		name, kw := uidName(t, u)
		rows = append(rows, []string{name, "`" + kw + "`", "`" + u + "`", "N", "Y"})
	}
	blocks["dimse-sop-classes"] = dcsTable([]string{"SOP Class", "Keyword", "UID", "SCU", "SCP"}, rows)

	// Storage SOP classes, numerical UID order.
	var st []string
	for _, u := range sc.AcceptedAbstractSyntaxes {
		if storage[u] {
			st = append(st, u)
		}
	}
	sort.Slice(st, func(i, j int) bool { return uidLess(st[i], st[j]) })
	rows = nil
	for _, u := range st {
		name, kw := uidName(t, u)
		// SCU: C-MOVE/C-GET sub-operations and POST /control/v1/send send
		// stored instances of any class.
		rows = append(rows, []string{name, "`" + kw + "`", "`" + u + "`", "U; LL; L", "Y", "Y", "Y"})
	}
	blocks["storage-sop-classes"] = dcsTable([]string{"SOP Class", "Keyword", "UID", "Transfer Syntax Sets", "SCU", "SCP", "Archive"}, rows)

	// Transfer syntaxes accepted for storage, by set.
	ts := append([]string(nil), dcm.StorageSyntaxes...)
	sort.SliceStable(ts, func(i, j int) bool { return tsSetOrder[tsSet(t, ts[i])] < tsSetOrder[tsSet(t, ts[j])] })
	rows = nil
	setName := map[string]string{"U": "Uncompressed (U)", "LL": "Lossless compressed (LL)", "L": "Lossy compressed (L)"}
	for _, u := range ts {
		name, _ := uidName(t, u)
		rows = append(rows, []string{setName[tsSet(t, u)], name, "`" + u + "`"})
	}
	blocks["transfer-syntaxes"] = dcsTable([]string{"Transfer Syntax Set", "Transfer Syntax Name", "UID"}, rows)

	// Association parameters (defaults).
	d := DefaultConfig()
	icuid, ivn := dicomuid.Implementation(dicomuid.LibreDCM, "0.2.0")
	blocks["association-parameters"] = dcsTable([]string{"Name", "Value"}, [][]string{
		{"Application Context Name", "`" + pdu.ApplicationContextName + "`"},
		{"Implementation Class UID", "`1.3.6.1.4.1.67686.1.1.1.<major>.<minor>` (version 0.2.x: `" + icuid + "`)"},
		{"Implementation Version Name", "`LIBREDCM_<major>_<minor>` (version 0.2.x: `" + ivn + "`)"},
		{"Maximum PDU length received", fmt.Sprintf("Default: %d bytes (`max_pdu_length`)", d.MaxPDULength)},
		{"Association negotiation timeout (acceptor)", fmt.Sprintf("Default: %s (`handshake_timeout`)", d.HandshakeTimeout)},
		{"Idle timeout per PDU read or write", fmt.Sprintf("Default: %s (`idle_timeout`)", d.IdleTimeout)},
		{"Maximum number of simultaneous associations as acceptor", fmt.Sprintf("Default: %d per node (`max_associations`)", d.MaxAssociations)},
		{"Maximum number of simultaneous associations as initiator", "One per running C-MOVE or control-API send (not limited separately)"},
		{"Maximum number of outstanding asynchronous operations", "1 (asynchronous operations window not negotiated)"},
		{"File Meta Information Version", "`00\\01`"},
		{"C-FIND result limit", fmt.Sprintf("Default: %d matches (`find_limit`)", d.FindLimit)},
		{"C-GET buffer limit", fmt.Sprintf("Default: %d bytes (`get_max_bytes`)", d.GetMaxBytes)},
	})

	blocks["configuration"] = dcsConfigTable(t)

	// C-FIND keys per level.
	ret := returnedKeys(t)
	for _, lvl := range []index.Level{index.Patient, index.Study, index.Series, index.Image} {
		var kws []string
		for kw := range queryKeys {
			_, use := keyFilter(lvl, kw, []string{"X"}, queryOptions{caseInsensitivePN: true})
			if use == keyMatches || ret[lvl][kw] {
				kws = append(kws, kw)
			}
		}
		sort.Slice(kws, func(i, j int) bool { return tagString(kws[i]) < tagString(kws[j]) })
		rows = nil
		for _, kw := range kws {
			def := queryKeys[kw]
			_, use := keyFilter(lvl, kw, []string{"X"}, queryOptions{caseInsensitivePN: true})
			m, r := use == keyMatches, ret[lvl][kw]
			rows = append(rows, []string{attrName(t, kw), tagString(kw), "`" + kw + "`", matchingType(def.kind, m, r), keyComment(def.kind, m, r)})
		}
		blocks["query-keys-"+strings.ToLower(string(lvl))] = dcsTable([]string{"Attribute Name", "Tag", "Keyword", "Matching Type", "Comments"}, rows)
	}
	return blocks
}

// dcsConfigTable lists every DICOM listener setting of gateway.Config with
// its YAML key (under `dicom:`), environment variable and default.
func dcsConfigTable(t *testing.T) string {
	t.Helper()
	d := reflect.ValueOf(DefaultConfig())
	typ := d.Type()
	var rows [][]string
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		key := strings.Split(f.Tag.Get("yaml"), ",")[0]
		if key == "" || key == "-" {
			t.Fatalf("Config.%s has no yaml key", f.Name)
		}
		env := "(YAML only)"
		if e := f.Tag.Get("env"); e != "" {
			env = "`" + strings.Split(e, ",")[0] + "`"
		}
		if key == "remote_aes" {
			env = "`LIBRE_DCM_REMOTE_AES` (`NAME=HOST:PORT[:AE]`, comma-separated)"
		}
		var def string
		switch v := d.Field(i).Interface().(type) {
		case string:
			def = "`" + v + "`"
			if v == "" {
				def = "(empty)"
			}
		case bool, int, int64, uint32:
			def = fmt.Sprintf("`%v`", v)
		case time.Duration:
			def = "`" + v.String() + "`"
		case []string:
			def = "(empty)"
		case map[string]RemoteAE:
			def = "(none)"
		default:
			t.Fatalf("Config.%s: type %T not handled", f.Name, v)
		}
		rows = append(rows, []string{"`" + key + "`", env, def})
	}
	return dcsTable([]string{"Setting (`dicom:`)", "Environment variable", "Default"}, rows)
}

// TestConformanceStatementMatchesCode holds the DICOM Conformance Statement's
// tables (SOP classes, transfer syntaxes, association parameters, C-FIND keys
// per level) to the running code.
// Verifies: DCM-SRS-101
func TestConformanceStatementMatchesCode(t *testing.T) {
	path, err := filepath.Abs(dcsPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path) // #nosec G304 -- fixed repository path
	if err != nil {
		t.Fatal(err)
	}
	doc := string(raw)
	want := dcsExpected(t)
	seen := map[string]bool{}
	var problems []string
	out := dcsBlockRe.ReplaceAllStringFunc(doc, func(m string) string {
		p := dcsBlockRe.FindStringSubmatch(m)
		name, body, endName := p[2], p[3], p[5]
		if name != endName {
			problems = append(problems, fmt.Sprintf("block %q ends with %q", name, endName))
			return m
		}
		seen[name] = true
		exp, ok := want[name]
		if !ok {
			problems = append(problems, fmt.Sprintf("block %q is not generated from code", name))
			return m
		}
		if body != exp {
			problems = append(problems, fmt.Sprintf("block %q differs from the code:\n--- document\n%s--- code\n%s", name, body, exp))
		}
		return p[1] + exp + p[4]
	})
	for name := range want {
		if !seen[name] {
			problems = append(problems, fmt.Sprintf("block %q missing from the document", name))
		}
	}
	if *updateDCS || os.Getenv("LIBRE_DCM_UPDATE_DCS") == "1" {
		if out != doc {
			if err := os.WriteFile(path, []byte(out), 0o644); err != nil { // #nosec G306 -- a repository document, world-readable by design
				t.Fatal(err)
			}
			t.Logf("rewrote generated tables in %s", path)
		}
		return
	}
	for _, p := range problems {
		t.Error(p)
	}
}
