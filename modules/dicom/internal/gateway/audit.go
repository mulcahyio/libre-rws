package gateway

import (
	"context"
	"log/slog"
	"sort"

	"github.com/godicom-dev/gonetdicom/ae"

	"rws/modules/dicom/internal/index"
)

// Audit events follow the DICOM audit message vocabulary (PS3.15 A.5) so they
// can later be emitted as ATNA syslog messages; for now they are structured
// log lines with audit=true, which any log shipper can route separately.
const (
	auditInstancesStored   = "DICOMInstancesTransferred.received"
	auditQuery             = "Query"
	auditInstancesExported = "DICOMInstancesTransferred.sent"
	auditAssocRejected     = "SecurityAlert.association-rejected"
)

// audit writes one audit record. Outcome is "success", "minor-failure" or
// "serious-failure". Who is taken from the association in ctx.
func (g *Gateway) audit(ctx context.Context, event, outcome string, attrs ...any) {
	if g.cfg.AuditLogOff {
		return
	}
	base := []any{"audit", true, "event", event, "outcome", outcome}
	if a, ok := ae.AssociationFromContext(ctx); ok {
		remote := ""
		if a.Remote != nil {
			remote = a.Remote.String()
		}
		base = append(base, "calling_ae", a.CallingAE, "called_ae", a.CalledAE, "remote", remote)
	}
	g.log.LogAttrs(ctx, slog.LevelInfo, "audit", argsToAttrs(append(base, attrs...))...)
}

func argsToAttrs(args []any) []slog.Attr {
	var out []slog.Attr
	for i := 0; i+1 < len(args); i += 2 {
		k, _ := args[i].(string)
		out = append(out, slog.Any(k, args[i+1]))
	}
	return out
}

// studiesAndPatients summarises retrieved rows for an export audit record.
func studiesAndPatients(rows []index.Row) (studies, patients []string) {
	s, p := map[string]bool{}, map[string]bool{}
	for _, r := range rows {
		s[r["StudyInstanceUID"]] = true
		if r["PatientID"] != "" {
			p[r["PatientID"]] = true
		}
	}
	for k := range s {
		studies = append(studies, k)
	}
	for k := range p {
		patients = append(patients, k)
	}
	sort.Strings(studies)
	sort.Strings(patients)
	return studies, patients
}
