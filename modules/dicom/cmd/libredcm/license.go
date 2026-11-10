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
func licenseCommand(cfgPath string, status, request bool, activateKey, installFile, key, site, ackBy string) int {
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
	case ackBy != "":
		a, err := lic.Acknowledge(ackBy)
		if err != nil {
			fmt.Fprintln(os.Stderr, "LibreDCM: license-ack:", err)
			return 1
		}
		fmt.Printf("acknowledged by %s at %s (license state: %s); reminders and the configuration block resume after %s unless the license is renewed\n",
			a.By, a.At.Format(time.RFC3339), a.State, a.At.Add(licensing.AckValidity).Format(time.RFC3339))
		return 0
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
		b, err := os.ReadFile(installFile) // #nosec G304 -- file named by the administrator on the command line
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
	fmt.Println("DICOM services: running (licensing never stops them)")
	if lic.AckRequired() {
		fmt.Println("administrator acknowledgement required: LibreDCM -license-ack NAME")
	}
	if licensing.Licensed(st) {
		return 0
	}
	return 2
}
