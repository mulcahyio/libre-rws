# LibreDCM — Workstation Install

| | |
|---|---|
| **Product** | Libre RWS (Radiology Workflow System) — LibreDCM, the DICOM module (formerly `rws-dicom`) |
| **Developed by** | Patrick Mulcahy, Mulcahy.io |
| **Contact** | dev@mulcahy.io |
| **Applies to** | LibreDCM 0.2.0-beta.1, single node on one PC |

This folder installs LibreDCM as a Docker container on a local workstation.
It runs one node with an embedded SQLite index. Images are stored on the PC
or in an S3-compatible store, chosen at install. You don't need a database
server or message bus. LibreDCM accepts C-ECHO, C-STORE, C-FIND, C-MOVE and
C-GET, optionally over DICOM TLS.

## Requirements

- Windows 10/11 with **Docker Desktop** (WSL 2 backend), or macOS/Linux with
  Docker Engine and Compose v2.
- About 2 GB free for the build, plus space for your images.
- Network access the first time, to pull the Go and distroless base images and
  the Go modules.

## Install (Windows)

1. Start Docker Desktop and wait for *Engine running*.
2. Open this folder (`deploy\workstation` in the libre-rws repository) and
   double-click **`install.cmd`**, or run it from PowerShell:

   ```powershell
   powershell -ExecutionPolicy Bypass -File .\LibreDCM.ps1 install
   ```

## Install (macOS / Linux)

```bash
./LibreDCM.sh install
```

## Install-time choices

The first `install` asks four questions and writes the answers to `.env`:

| Question | Choices | Settings |
|---|---|---|
| AE title | 1–16 characters | `LIBRE_DCM_AE_TITLE` |
| Storage | this PC (Docker volume), this PC (a folder), S3 / S3-compatible (AWS, MinIO, Ceph, NAS) | `LIBRE_DCM_STORAGE_DRIVER`, `LIBRE_DCM_DATA`, `LIBRE_DCM_STORAGE_S3_*` |
| TLS | off, self-signed (created by the installer), your own certificate files | `LIBRE_DCM_TLS_MODE`, `LIBRE_DCM_TLS_HOSTS`, `LIBRE_DCM_TLS_DIR` |
| Patient IDs | include or leave out of event-bus messages, and of the audit log | `LIBRE_DCM_EVENTS_OMIT_PATIENT_ID`, `LIBRE_DCM_AUDIT_OMIT_PATIENT_ID` |

Run `configure` to answer them again. For unattended installs, fill in `.env`
(copy it from `.env.example`) and run `install -Yes` (Windows) or
`LIBREDCM_YES=1 ./LibreDCM.sh install`.

- **TLS** covers both DICOM and the admin endpoint. *Self-signed* creates
  `tls.crt` / `tls.key` in the data area and keeps them on later runs, so
  peers that trusted the certificate keep working. Add the names and IPs
  other devices use in `LIBRE_DCM_TLS_HOSTS`. *Files* copies your certificate
  (PEM, full chain) and key into `.\tls`.
- **S3**: the index still lives on this PC; only images go to the bucket.
  `backup` then copies the index; use the store's own versioning for images.
- An **admin token** is generated on first install (`LIBRE_DCM_ADMIN_TOKEN`).

The installer then builds the image and checks four things: the container is
healthy, the admin endpoint answers, the DICOM port is reachable from the PC,
and a DICOM C-ECHO succeeds (over TLS when it is on). It prints the AE title,
IP address and port to enter on your modalities and viewers.

## License (official builds)

Official LibreDCM builds need a license from LibreKey, locked to this PC.
Source builds (compiled yourself from the open-source code) need none.

- **Online:** `install` asks for your license key (from the LibreKey portal)
  and activates this PC. Unattended: put it in `LIBRE_DCM_LICENSE_KEY`.
- **No internet on this PC:** answer `offline`. The installer writes
  `license-request.txt`. Upload it on your license page in the portal,
  download the license file, then run `license-install <file>` and `install`.
- **Renewal:** annual licenses renew automatically when the PC can reach the
  LibreKey server in the last 30 days; offline PCs need a new license file.
- **Expiry:** after the expiry date LibreDCM keeps working for the grace
  period (30 days), with warnings in the log. Then it **stops receiving new
  studies** (C-STORE is refused with "out of resources", so modalities keep
  the images and retry). C-ECHO, C-FIND, C-MOVE and C-GET keep working, so
  all existing studies stay available.
- **Moving to another PC:** deactivate the old PC in the portal, then
  activate the new one.

```powershell
.\LibreDCM.ps1 license                    # status
.\LibreDCM.ps1 activate LK-XXXXX-...      # activate online
.\LibreDCM.ps1 license-request            # request file for offline activation
.\LibreDCM.ps1 license-install FILE       # install a license file
```

Metrics: `libre_dcm_license_state` (0 valid, 1 grace, 2 not receiving) and
`libre_dcm_license_days_left`.

## Other settings

All settings are in **`.env`**. Edit it, then run `install` again. Your data is kept.

| Setting | Default | Meaning |
|---|---|---|
| `LIBRE_DCM_PORT` | `11112` | DICOM port on this PC |
| `LIBRE_DCM_BIND_IP` | `0.0.0.0` | `0.0.0.0` = reachable from the network; `127.0.0.1` = this PC only |
| `LIBRE_DCM_REMOTE_AES` | *(empty)* | C-MOVE destinations: `NAME=HOST:PORT[:AE]`, comma-separated |
| `LIBRE_DCM_ALLOWED_AES` | *(empty)* | Peers allowed to connect: `AE`, `AE@IP`, `AE@CIDR`; empty = any |
| `LIBRE_DCM_ALLOW_ANY_CALLED_AE` | `false` | Accept any called AE title (useful while testing) |
| `LIBRE_DCM_ACCEPT_ANY_SOP_CLASS` | `false` | Accept non-standard SOP classes |
| `LIBRE_DCM_ADMIN_PORT` | `8081` | Health/metrics, bound to 127.0.0.1 only |
| `LIBRE_DCM_LOG_LEVEL` / `LIBRE_DCM_LOG_FORMAT` | `info` / `text` | Logging |
| `LIBRE_DCM_EVENTS_DRIVER` | `log` | `none`, `log`, or `nats` (also starts a local NATS server) |

**C-MOVE to a viewer on the same PC:** use `host.docker.internal` as the host,
for example `LIBRE_DCM_REMOTE_AES=VIEWER1=host.docker.internal:11113`.
On Linux with a host firewall (ufw, firewalld), traffic from containers to
the host is often blocked, so the C-MOVE times out. Allow the viewer's port
from the Docker networks, for example
`sudo ufw allow from 172.16.0.0/12 to any port 11113 proto tcp`.

**Data location:** keep the data folder on a local disk. The installer refuses
network paths and mapped network drives, because the SQLite index is not safe
on them. The Docker volume (the default) is the fastest option on Windows.

**Firewall:** if other machines can't connect, allow the DICOM port through
Windows Defender Firewall (inbound TCP).

## Day-to-day

```powershell
.\LibreDCM.ps1 status      # state and version
.\LibreDCM.ps1 verify      # health + C-ECHO
.\LibreDCM.ps1 logs        # follow the log
.\LibreDCM.ps1 restart
.\LibreDCM.ps1 backup      # index snapshot + images to .\backups\<timestamp>
```

**Backups:** `backup` is safe while LibreDCM is running. It first takes a
point-in-time snapshot of the index inside the container (`LibreDCM -backup`),
then copies the snapshot and the stored images. Images are written once and
never changed, so every image the snapshot lists is in the copy.

Health endpoints: `/readyz`, `/healthz` (open), `/metrics`, `/version` (need
the admin token) on `127.0.0.1:8081`, over HTTPS when TLS is on. The
container restarts on its own after a crash or a reboot.

## Upgrade

Update the source (`git pull`, or unpack the new release over the old one),
then:

```powershell
.\LibreDCM.ps1 upgrade
```

The new image is built while the old one keeps running. The upgrade then
stops LibreDCM, backs up the index to `.\backups\index-<timestamp>`, starts
the new version and verifies it. Stored images and the index carry over.

**Upgrading from 0.1.x (`rws-dicom`):** copy your old `.env` into this folder
and run `upgrade`. The installer renames the `RWS_*` settings (the old file
is kept as `.env.bak-<timestamp>`), removes the old `rws-dicom` container and
keeps using your existing data volume (`rws-dicom_rws-dicom-data`). The installer asks the
install-time questions once. LibreDCM still reads `RWS_*` settings (with a
warning in the log) and still publishes the old `rws.dicom.*` event subjects
(`LIBRE_DCM_EVENTS_LEGACY_SUBJECTS=false` turns that off).

## Uninstall

```powershell
.\LibreDCM.ps1 uninstall               # removes container + image, keeps data
.\LibreDCM.ps1 uninstall -RemoveData   # also deletes the Docker data volume (asks first)
```

A bind-mounted `LIBRE_DCM_DATA` folder and images in an S3 bucket are never
deleted automatically.

## Troubleshooting

| Symptom | Check |
|---|---|
| *Port already in use* | Another DICOM program uses the port. Change `LIBRE_DCM_PORT`. |
| Modality gets *association rejected* | The called AE on the modality must match `LIBRE_DCM_AE_TITLE` exactly; if `LIBRE_DCM_ALLOWED_AES` is set, the modality's AE (and IP) must be in it. |
| Modality can't connect after enabling TLS | The modality must use DICOM TLS too and trust the certificate (self-signed: import `tls.crt`). |
| C-MOVE fails with *unknown destination* | Add the destination to `LIBRE_DCM_REMOTE_AES` and restart. |
| *Not healthy* | `.\LibreDCM.ps1 logs`. With S3 storage, readiness waits for the bucket; with `nats`, for NATS. |

## Security note

The admin endpoint is bound to 127.0.0.1 and needs the admin token for
`/metrics` and `/version`. Turn on TLS and set `LIBRE_DCM_ALLOWED_AES` before
exposing the DICOM port beyond a trusted network.

---
Libre RWS — developed by Patrick Mulcahy, Mulcahy.io · dev@mulcahy.io
