package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/mulcahyio/libre-rws/modules/dicom/internal/licensing"
)

func parseBuildDate() time.Time {
	t, _ := time.Parse("2006-01-02", buildDate)
	return t
}

// licenseCommand implements the -license-* flags used by the installer.
func licenseCommand(cfgPath string, status, request bool, activateKey, installFile, key, site string) int {
	if !licensing.Enforced() {
		fmt.Println("source build: no license needed")
		return 3
	}
	cfg, err := loadConfig(cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "LibreDCM:", err)
		return 1
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	lic, err := licensing.New(cfg.License, version, parseBuildDate(), log, nil)
	if err != nil {
		fmt.Fprintln(os.Stderr, "LibreDCM: license:", err)
		return 1
	}
	m := lic.Manager()
	switch {
	case request:
		fmt.Print(m.Request(key, site).Encode())
		return 0
	case activateKey != "":
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		st, err := m.Activate(ctx, activateKey, site)
		if err != nil {
			fmt.Fprintln(os.Stderr, "activation failed:", err)
			return 1
		}
		fmt.Println("activated:", st.Message)
		return 0
	case installFile != "":
		b, err := os.ReadFile(installFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		st, err := m.Install(string(b))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Println("license installed:", st.Message)
		return 0
	}
	st := m.Status()
	fmt.Printf("state: %s\n%s\nmachine: %s\n", st.State, st.Message, m.Fingerprint())
	if st.License != nil {
		fmt.Printf("customer: %s\nlicense key: %s\nterm: %s\n", st.License.Customer, st.License.Key, st.License.Term)
	}
	if st.CanReceive() {
		return 0
	}
	return 2
}
