package ae_test

import (
	"net"
	"testing"

	"github.com/godicom-dev/gonetdicom/ae"
	"github.com/godicom-dev/gonetdicom/pdu"
)

// RWS fork: AcceptCallingAE rejects requestors that are not allowed.
func TestAcceptCallingAE(t *testing.T) {
	var gotAE string
	var gotRemote net.Addr
	addr := serveSCP(t, ae.ServerConfig{
		AETitle: "SCP",
		AcceptCallingAE: func(calling string, remote net.Addr) bool {
			gotAE, gotRemote = calling, remote
			return calling == "GOOD"
		},
	})

	rq := associateRQ("SCP")
	rq.CallingAETitle = "BAD"
	requireReject(t, associate(t, addr, rq), pdu.RejectSourceServiceUser, pdu.RejectReasonCallingAENotRecognized)
	if gotAE != "BAD" || gotRemote == nil {
		t.Fatalf("callback got %q %v", gotAE, gotRemote)
	}

	rq = associateRQ("SCP")
	rq.CallingAETitle = "GOOD  " // padding is removed before the check
	if _, ok := associate(t, addr, rq).(*pdu.AAssociateAC); !ok {
		t.Fatal("allowed calling AE was not accepted")
	}
	if gotAE != "GOOD" {
		t.Fatalf("callback got %q, want padding removed", gotAE)
	}
}
