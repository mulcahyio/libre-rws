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

The first `install` asks five groups of questions and writes the answers to `.env`:

| Question | Choices | Settings |
|---|---|---|
| AE title | 1–16 characters | `LIBRE_DCM_AE_TITLE` |
| Storage | this PC (Docker volume), this PC (a folder), S3 / S3-compatible (AWS, MinIO, Ceph, NAS) | `LIBRE_DCM_STORAGE_DRIVER`, `LIBRE_DCM_DATA`, `LIBRE_DCM_STORAGE_S3_*` |
| TLS | off, self-signed (created by the installer), your own certificate files | `LIBRE_DCM_TLS_MODE`, `LIBRE_DCM_TLS_HOSTS`, `LIBRE_DCM_TLS_DIR` |
| Patient IDs | include or leave out of event-bus messages, and of the audit log | `LIBRE_DCM_EVENTS_OMIT_PATIENT_ID`, `LIBRE_DCM_AUDIT_OMIT_PATIENT_ID` |
| Patient ID conflicts | hold for review (default), refuse, or accept as a separate study; hide held images (default) or show them flagged; prefix their patient ID with `B000_` or not (default) | `LIBRE_DCM_PATIENT_CONFLICT_POLICY`, `LIBRE_DCM_PATIENT_CONFLICT_VISIBILITY`, `LIBRE_DCM_PATIENT_CONFLICT_COERCE_PATIENT_ID` |

Run `configure` to answer them again. For unattended installs, fill in `.env`
(copy it from `.env.example`) and run `install -Yes` (Windows) or
`LIBREDCM_YES=1 ./LibreDCM.sh install`.

- **TLS** covers both DICOM and the admin endpoint. *Self-signed* creates
  `tls.crt` / `tls.key` in the data area and keeps them on later runs, so
  peers that trusted the certificate keep working. Add the names and IPs
  other devices use in `LIBRE_DCM_TLS_HOSTS`. *Files* copies your certificate
  (PEM, full chain) and key into `.\tls`.
- **Patient ID conflicts**: when images arrive for a study that is already
  stored under a different patient ID (for example the technologist picked
  the wrong worklist entry, corrected it and sent again), LibreDCM never moves
  the stored study to the new patient. By default it keeps the new images
  apart and hidden until an administrator resolves them in Libre WT (correct
  the new images, correct the stored study, or reject). Libre WT needs
  `LIBRE_DCM_ADMIN_TOKEN` to be set; the control API refuses every request
  without it.
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
- **Expiry:** licensing **never stops DICOM services**. Receiving, searching
  and retrieving images keep working in every license state, so patient care
  is never interrupted. After the expiry date there is a 30-day grace period
  with a daily warning in the log. After that, LibreDCM is *unlicensed*:
  - an error is logged **every hour**;
  - configuration reloads are blocked;
  - `/version` and the metrics report the state.
  This continues until an administrator acknowledges it with
  `license-ack "YOUR NAME"`. The acknowledgement is written to the audit log
  and silences the reminders for 24 hours; then they start again until the
  license is renewed. Updates and support need a valid license.
- **Moving to another PC:** deactivate the old PC in the portal, then
  activate the new one.

```powershell
.\LibreDCM.ps1 license                    # status
.\LibreDCM.ps1 activate LK-XXXXX-...      # activate online
.\LibreDCM.ps1 license-request            # request file for offline activation
.\LibreDCM.ps1 license-install FILE       # install a license file
.\LibreDCM.ps1 license-ack "Jane Admin"   # acknowledge the unlicensed notice (24 hours)
```

Metrics: `libre_dcm_license_state` (0 valid, 1 grace, 2 unlicensed),
`libre_dcm_license_days_left` and `libre_dcm_license_ack_required`.

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

**Libre WT (web console):** Libre WT shows this node on its fleet page,
with a dashboard and a status page, and handles Study Exceptions. One Libre
WT can manage several LibreDCM nodes: give each node its own admin token and
list the nodes in the Libre WT configuration file (`instances:`, see
`modules/wt/README.md`). Libre WT remembers the node ID and AE title of each
node. If you replace a node or change its AE title, Libre WT shows a warning
and an administrator accepts the new identity in the console. Libre WT does
not start, stop or restart LibreDCM: use the commands above.

**Control API (for Libre WT):** with the admin token, `/control/v1` on the
same port shows the node's status and counters (`/control/v1/status`), the
running settings with every secret hidden (`/control/v1/config`), the remote
AEs and the allow-list in force, and the most recent audit records and log
lines held in memory (`/control/v1/audit`, `/control/v1/logs`). These views
only read. The in-memory records are lost at restart; the container log stays
the full record.

The control API also has actions. Each one needs the name of the person
(`operator`), a `reason` and a `request_id` (16 to 64 characters, new for
each action; sending the same one again returns the first result and does
nothing twice). Each action is written to the audit log before it starts
and again with its outcome.

| Action | What it does |
|---|---|
| `POST /control/v1/echo` | C-ECHO test from LibreDCM to a configured remote AE (`remote_ae`) or to `host`, `port`, `ae_title`. Sends no patient data |
| `POST /control/v1/reload/plan`, `POST /control/v1/reload` | Shows what a configuration reload would change, then reloads (same as `SIGHUP`). The reload needs `confirm` and the `plan_digest` from the plan. A broken configuration is reported and changes nothing |
| `GET /control/v1/license`, `POST /control/v1/license/ack`, `/request`, `/install`, `/activate` | Licence status and the same functions as `license-status`, `license-ack`, `license-request`, `license-install`, `license-activate`. Licensing never stops DICOM services |
| `POST /control/v1/backup`, `GET /control/v1/backups` | Index snapshot into one fixed folder, and the list of snapshots there. Off until you set the folder (below) |
| `GET /control/v1/studies` | Finds patients, studies, series or images in the index, with the matching rules of C-FIND. Needs at least one matching key and the header `X-Libre-Operator` (the name of the person). At most 200 rows per page. Each search is written to the audit log |
| `POST /control/v1/send/plan`, `POST /control/v1/send` | Sends a study (or chosen series or images) to a configured remote AE. You name the destination, the study and the Patient ID; the plan shows what would go where; the send needs `confirm` and the `plan_digest` from the plan. Images held as a patient conflict are never sent. One send at a time, at most 5000 images |
| `POST /control/v1/config/plan`, `POST /control/v1/config`, `GET /control/v1/config/history`, `POST /control/v1/config/rollback/plan`, `POST /control/v1/config/rollback` | Changes the C-MOVE destinations (`dicom.remote_aes`) and the allow-list (`dicom.allowed_aes`) without a restart, and puts the previous version back. No other setting can be changed this way |

**Changing destinations and the allow-list through the control API.**
LibreDCM writes such a change to its own file in the data volume
(`config-overlay.yaml`), never to `.env` or `config.yaml`. Things to know:

- A setting changed this way replaces the value from `config.yaml` as a
  whole. `GET /control/v1/config` shows `overlay` as its source.
- A value set in `.env` (`LIBRE_DCM_ALLOWED_AES`, `LIBRE_DCM_REMOTE_AES`)
  still wins and can not be changed through the API. The workstation
  installer writes these two settings to `.env`: to manage them from Libre
  WT, leave them empty in `.env`.
- Emptying the allow-list, removing a destination, pointing a destination
  at another address, or a new allow-list that would refuse a modality that
  was active in the last 24 hours gives a warning that must be
  acknowledged.
- One previous version is kept (`config-overlay.yaml.prev`) for the
  rollback.
- To drop every change made through the API: stop LibreDCM, delete
  `config-overlay.yaml` in the data volume, start it again.
- If that file is damaged or was edited by hand with other settings in it,
  LibreDCM does not start and says so in its log. Delete the file (the
  settings of `.env` and `config.yaml` then apply) and start again.
- To turn the function off, set `control.overlay_file: ""` in
  `config.yaml`.

**Backups through the control API** are off by default. To turn them on,
add this line to `.env` and run `restart`:

```
LIBRE_DCM_CONTROL_BACKUP_DIR=/var/lib/libredcm/backups
```

The folder is inside the container's data volume. LibreDCM chooses the file
name (`index-<node>-<UTC time>.db`); a caller can not choose or change the
path, and no file is ever overwritten. The snapshot holds the **index only**
(patient and study data, no images). Copy it and the images to a safe place
with `backup` as before; snapshots are not removed automatically, and they
can not be downloaded or deleted through the API.

Example:

```powershell
curl.exe -s -H "Authorization: Bearer $env:LIBRE_DCM_ADMIN_TOKEN" http://127.0.0.1:8081/control/v1/status
```

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
