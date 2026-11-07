# Libre DCM — Workstation Install

| | |
|---|---|
| **Product** | Libre RWS (Radiology Workflow System) — Libre DCM, the DICOM module (binary `rws-dicom`, formerly RWS DICOM) |
| **Developed by** | Patrick Mulcahy, Mulcahy.io |
| **Contact** | dev@mulcahy.io |
| **Applies to** | Libre DCM (`rws-dicom`) 0.1.1-beta.1, single node on one PC |

This folder installs the DICOM module as a Docker container on a local
workstation. It runs one node with an embedded SQLite index and local
storage. You don't need a database server or message bus. The module accepts
C-ECHO, C-STORE, C-FIND, C-MOVE and C-GET.

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
   powershell -ExecutionPolicy Bypass -File .\rws-dicom.ps1 install
   ```

   You can set the main options on the command line:

   ```powershell
   .\rws-dicom.ps1 install -AETitle RWS_WS1 -DicomPort 11112 -DataDir C:/RWS/dicom-data
   ```

The first build takes a few minutes. The installer then checks four things:
the container is healthy, the admin endpoint answers, the DICOM port is
reachable from the PC, and a DICOM C-ECHO succeeds. When it finishes it prints
the AE title, IP address and port to enter on your modalities and viewers.

## Install (macOS / Linux)

```bash
./rws-dicom.sh install
```

## Configure

The only settings file is **`.env`**. The installer creates it from
`.env.example` on first run. Edit it, then run `install` again (or
`restart`). Your data is kept.

| Setting | Default | Meaning |
|---|---|---|
| `RWS_DICOM_AE_TITLE` | `RWS_DICOM` | AE title other devices call (1–16 characters) |
| `RWS_DICOM_PORT` | `11112` | DICOM port on this PC |
| `RWS_BIND_IP` | `0.0.0.0` | `0.0.0.0` = reachable from the network; `127.0.0.1` = this PC only |
| `RWS_DICOM_REMOTE_AES` | *(empty)* | C-MOVE destinations: `NAME=HOST:PORT[:AE]`, comma-separated |
| `RWS_DICOM_ALLOW_ANY_CALLED_AE` | `false` | Accept any called AE title (useful while testing) |
| `RWS_DICOM_ACCEPT_ANY_SOP_CLASS` | `false` | Accept non-standard SOP classes |
| `RWS_DATA` | *(empty)* | Empty = Docker volume `rws-dicom-data`; or a **local** folder |
| `RWS_ADMIN_PORT` | `8081` | Health/metrics, bound to 127.0.0.1 only |
| `RWS_LOG_LEVEL` / `RWS_LOG_FORMAT` | `info` / `text` | Logging |
| `RWS_EVENTS_DRIVER` | `log` | `none`, `log`, or `nats` (also starts a local NATS server) |

**C-MOVE to a viewer on the same PC:** use `host.docker.internal` as the host,
for example `RWS_DICOM_REMOTE_AES=VIEWER1=host.docker.internal:11113`.
On Linux with a host firewall (ufw, firewalld), traffic from containers to
the host is often blocked, so the C-MOVE times out. Allow the viewer's port
from the Docker networks, for example
`sudo ufw allow from 172.16.0.0/12 to any port 11113 proto tcp`.

**Data location:** keep data on a local disk. The installer refuses network
paths and mapped network drives, because the SQLite index is not safe on them.
The Docker volume (the default) is the fastest option on Windows.

**Firewall:** if other machines can't connect, allow the DICOM port through
Windows Defender Firewall (inbound TCP).

## Day-to-day

```powershell
.\rws-dicom.ps1 status      # state and version
.\rws-dicom.ps1 verify      # health + C-ECHO
.\rws-dicom.ps1 logs        # follow the log
.\rws-dicom.ps1 restart
.\rws-dicom.ps1 backup      # copy images + index to .\backups\<timestamp>
```

**Backups:** `backup` is safe while the module is running. It first takes a
point-in-time snapshot of the index inside the container (`rws-dicom -backup`),
then copies the snapshot and the stored images. Images are written once and
never changed, so every image the snapshot lists is in the copy. The latest
snapshot is kept in the data area at `backup/index.db` (about the size of the
index).

Health endpoints: `http://127.0.0.1:8081/readyz`, `/healthz`, `/metrics`,
`/version`. The container restarts on its own after a crash or a reboot
(once Docker Desktop is running).

## Upgrade

Update the source (`git pull`, or unpack the new release over the old one),
then:

```powershell
.\rws-dicom.ps1 upgrade
```

The new image is built while the old one keeps running. The upgrade then
stops the module, backs up the index to `.\backups\index-<timestamp>`, starts
the new version and verifies it. Stored images and the index carry over.

## Uninstall

```powershell
.\rws-dicom.ps1 uninstall               # removes container + image, keeps data
.\rws-dicom.ps1 uninstall -RemoveData   # also deletes the Docker data volume (asks first)
```

A bind-mounted `RWS_DATA` folder is never deleted automatically.

## Troubleshooting

| Symptom | Check |
|---|---|
| *Port already in use* | Another DICOM program uses the port. Change `RWS_DICOM_PORT`. |
| Modality gets *association rejected* | The called AE on the modality must match `RWS_DICOM_AE_TITLE` exactly, or set `RWS_DICOM_ALLOW_ANY_CALLED_AE=true`. |
| C-MOVE fails with *unknown destination* | Add the destination to `RWS_DICOM_REMOTE_AES` and restart. |
| *Not healthy* | `.\rws-dicom.ps1 logs`. With `RWS_EVENTS_DRIVER=nats`, readiness waits for NATS. |

## Security note

This is a workstation build. The admin endpoint is unauthenticated, so it is
bound to 127.0.0.1. DICOM traffic is unencrypted. Don't expose the DICOM port
beyond a trusted network until AE allow-listing and TLS land (see the roadmap
in `docs/DEVELOPMENT.md`).

---
Libre RWS — developed by Patrick Mulcahy, Mulcahy.io · dev@mulcahy.io
