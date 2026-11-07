package main

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/godicom-dev/gonetdicom/ae"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/dcm"
)

// echo opens an association to addr, sends a C-ECHO (Verification SOP Class)
// and releases. It lets installers and operators prove the DICOM listener is
// reachable without needing DCMTK or another external tool:
//
//	LibreDCM -echo 127.0.0.1:11112 -echo-ae LIBREDCM
func echo(addr, calledAE string) int {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	scu, err := ae.Dial(ctx, ae.Config{
		AETitle: "LIBREDCM_ECHO",
		PresentationContexts: []ae.PresentationContext{
			{AbstractSyntax: "1.2.840.10008.1.1", TransferSyntaxes: []string{dcm.ImplicitVRLittleEndian}},
		},
	}, addr, calledAE)
	if err != nil {
		fmt.Fprintf(os.Stderr, "C-ECHO %s (%s): association failed: %v\n", addr, calledAE, err)
		return 1
	}
	defer func() { _ = scu.Release(ctx) }()
	if err := scu.CEcho(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "C-ECHO %s (%s): %v\n", addr, calledAE, err)
		return 1
	}
	fmt.Printf("C-ECHO %s (%s): success\n", addr, calledAE)
	return 0
}
