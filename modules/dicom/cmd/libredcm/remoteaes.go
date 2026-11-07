package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/gateway"
)

// parseRemoteAEs reads C-MOVE destinations from one environment variable so a
// container can be configured without mounting a config file:
//
//	LIBRE_DCM_REMOTE_AES="VIEWER1=192.168.1.20:11112, PACS_B=pacs-b:104:PACSB"
//
// Each entry is NAME=HOST:PORT[:CALLED_AE]. NAME is the AE title used in the
// C-MOVE request; CALLED_AE defaults to NAME.
func parseRemoteAEs(v string) (map[string]gateway.RemoteAE, error) {
	out := map[string]gateway.RemoteAE{}
	for _, item := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ';' }) {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		name, target, ok := strings.Cut(item, "=")
		name = strings.TrimSpace(name)
		parts := strings.Split(strings.TrimSpace(target), ":")
		if !ok || name == "" || len(parts) < 2 || len(parts) > 3 || parts[0] == "" {
			return nil, fmt.Errorf("LIBRE_DCM_REMOTE_AES: bad entry %q (want NAME=HOST:PORT[:AE_TITLE])", item)
		}
		port, err := strconv.Atoi(parts[1])
		if err != nil || port < 1 || port > 65535 {
			return nil, fmt.Errorf("LIBRE_DCM_REMOTE_AES: bad port in %q", item)
		}
		a := gateway.RemoteAE{Host: parts[0], Port: port}
		if len(parts) == 3 && parts[2] != "" {
			a.AETitle = parts[2]
		}
		if len(name) > 16 || (a.AETitle != "" && len(a.AETitle) > 16) {
			return nil, fmt.Errorf("LIBRE_DCM_REMOTE_AES: AE title longer than 16 characters in %q", item)
		}
		out[name] = a
	}
	return out, nil
}
