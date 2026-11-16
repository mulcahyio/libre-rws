package control

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
)

// Send (control-api-v1.md §5.2, DCM-SRS-092): a study, some of its series
// or some of its instances go to a configured destination over the send
// path of C-MOVE. The person names the patient as well as the study, sees
// a plan first, and the send runs only for the plan they approved.

const (
	auditExported = "DICOMInstancesTransferred.sent"

	// SendTimeout bounds one send (DCM-SRS-096). Instances not sent when it
	// expires are reported as failed.
	SendTimeout = 30 * time.Minute

	maxSendSeries = 500
)

var reUID = regexp.MustCompile(`^[0-9]+(\.[0-9]+)*$`)

// SendRequest is the body of POST /control/v1/send and .../send/plan.
type SendRequest struct {
	Envelope
	Destination       string   `json:"destination"`
	PatientID         string   `json:"patient_id"`
	StudyInstanceUID  string   `json:"study_instance_uid"`
	SeriesInstanceUID []string `json:"series_instance_uids,omitempty"`
	SOPInstanceUID    []string `json:"sop_instance_uids,omitempty"`
	PlanDigest        string   `json:"plan_digest,omitempty"`
	Confirm           bool     `json:"confirm,omitempty"`
}

// sendStatus maps the gateway's refusal codes to HTTP status codes.
func sendStatus(code string) int {
	switch code {
	case "unknown_destination", "study_not_found":
		return http.StatusNotFound
	}
	return http.StatusUnprocessableEntity
}

// gatewayRequest validates the form of a request and turns it into the
// gateway's. code is "" when it is acceptable.
func (r *SendRequest) gatewayRequest() (req gateway.SendRequest, code, msg string) {
	valid := func(u string) bool { return len(u) <= 64 && reUID.MatchString(u) }
	if r.StudyInstanceUID != "" && !valid(r.StudyInstanceUID) {
		return req, "invalid_uid", "study_instance_uid is not a DICOM UID"
	}
	if len(r.SeriesInstanceUID) > maxSendSeries || len(r.SOPInstanceUID) > gateway.MaxSendInstances {
		return req, "too_many_instances", "too many series or instances named in one send"
	}
	for _, u := range append(append([]string{}, r.SeriesInstanceUID...), r.SOPInstanceUID...) {
		if !valid(u) {
			return req, "invalid_uid", "a series or instance UID is not a DICOM UID"
		}
	}
	return gateway.SendRequest{Destination: r.Destination, StudyUID: r.StudyInstanceUID, PatientID: r.PatientID,
		SeriesUIDs: r.SeriesInstanceUID, SOPInstanceUIDs: r.SOPInstanceUID}, "", ""
}

// sendDigest is the digest of a send plan. It covers the destination's
// address, the patient, the study and every instance that would go out.
func sendDigest(p gateway.SendPlan) string {
	raw, _ := json.Marshal(p)
	sum := sha256.Sum256(raw)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func (a *API) sendPreview(ctx context.Context, r *SendRequest) (int, map[string]any) {
	if a.Data == nil {
		return http.StatusInternalServerError, map[string]any{"error": "send is not available on this node", "code": "internal"}
	}
	req, code, msg := r.gatewayRequest()
	if code != "" {
		return http.StatusUnprocessableEntity, map[string]any{"error": msg, "code": code}
	}
	ctx, cancel := context.WithTimeout(ctx, studiesTimeout)
	defer cancel()
	plan, err := a.Data.PlanSend(ctx, req)
	var reqErr *gateway.RequestError
	switch {
	case errors.As(err, &reqErr):
		return sendStatus(reqErr.Code), map[string]any{"error": reqErr.Msg, "code": reqErr.Code}
	case err != nil:
		return http.StatusInternalServerError, map[string]any{"error": "the send could not be planned; see the node's log", "code": "internal"}
	}
	return http.StatusOK, map[string]any{"plan": plan, "plan_digest": sendDigest(plan)}
}

// errSendPlanChanged stops a send whose plan is not the approved one.
var errSendPlanChanged = errors.New("send plan changed")

func (a *API) send(ctx context.Context, r *SendRequest) actionResult {
	if a.Data == nil {
		return refusal(http.StatusInternalServerError, "internal", "send is not available on this node", false)
	}
	if !r.Confirm {
		return refusal(http.StatusUnprocessableEntity, "not_confirmed",
			"send not confirmed: show the plan and set confirm=true after the person approves", true)
	}
	if r.PlanDigest == "" {
		return refusal(http.StatusUnprocessableEntity, "plan_digest_required",
			"plan_digest is required: ask for the plan first (POST /control/v1/send/plan)", true)
	}
	req, code, msg := r.gatewayRequest()
	if code != "" {
		return refusal(http.StatusUnprocessableEntity, code, msg, true)
	}
	// The gateway builds the plan again and sends exactly its instances,
	// and only when it is the plan the person approved.
	res, err := a.Data.Send(ctx, req, func(p gateway.SendPlan) error {
		if sendDigest(p) != r.PlanDigest {
			return errSendPlanChanged
		}
		return nil
	})
	ident := []any{"op", "control-send", "destination", r.Destination, "study", r.StudyInstanceUID}
	var reqErr *gateway.RequestError
	switch {
	case errors.Is(err, errSendPlanChanged):
		return refusal(http.StatusConflict, "plan_changed",
			"the study, the destination or the instances changed since the plan was made; nothing was sent. Ask for a new plan.", true, ident...)
	case errors.As(err, &reqErr):
		return refusal(sendStatus(reqErr.Code), reqErr.Code, reqErr.Msg, true, ident...)
	case err != nil:
		return refusal(http.StatusInternalServerError, "internal", "the send could not be started; nothing was sent. See the node's log.", false, ident...)
	}

	p := res.Plan
	patient := p.PatientID
	if a.Data.AuditOmitsPatientID() {
		patient = ""
	}
	attrs := []any{"op", "control-send", "destination", p.Destination.Name, "destination_host", p.Destination.Host,
		"destination_port", p.Destination.Port, "called_ae", p.Destination.AETitle,
		"studies", []string{p.StudyInstanceUID}, "patient_ids", []string{patient}, "series", p.SeriesCount,
		"instances", p.Instances, "completed", res.Completed, "failed", res.Failed, "warning", res.Warning}
	failed, warned := res.FailedUIDs, res.WarningUIDs
	if failed == nil {
		failed = []string{}
	}
	if warned == nil {
		warned = []string{}
	}
	body := map[string]any{"plan": p, "instances": p.Instances, "completed": res.Completed, "failed": res.Failed,
		"warning": res.Warning, "failed_sop_instance_uids": failed, "warning_sop_instance_uids": warned,
		"duration_ms": res.Duration.Milliseconds(),
		"note":        "Every instance of the plan that is not listed as failed or warning was accepted by the destination."}
	switch {
	case res.Failed == 0 && res.Warning == 0:
		body["outcome"] = "complete"
		return actionResult{status: http.StatusOK, outcome: outcomeSuccess, body: body, attrs: attrs}
	case res.Completed+res.Warning > 0:
		body["outcome"] = "partial"
		return actionResult{status: http.StatusOK, outcome: outcomeMinor, body: body, attrs: attrs}
	}
	// Nothing arrived: the answer must not read as a success.
	body["outcome"], body["code"] = "failed", "send_failed"
	body["error"] = "no instance was accepted by the destination; see the node's log"
	return actionResult{status: http.StatusBadGateway, outcome: outcomeSerious, body: body, attrs: attrs}
}
