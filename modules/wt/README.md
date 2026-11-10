# Libre WT (Watch Tower)

Web management console for LibreDCM, part of Libre RWS. Version 0.1:

- local accounts (argon2id), roles Viewer / Operator / Admin, first-run
  administrator creation, no default credentials;
- connection to one LibreDCM through its control API;
- **Study Exceptions**: images LibreDCM held because their Patient ID (or
  series/study) contradicts the archive. Admins correct the incoming images,
  correct the archived study, or reject the incoming images. Every edit shows
  exactly what will change and needs an explicit approval and a reason;
- audit log of logins, user changes, reviews and edits.

Design file: `docs/dhf/librewt/` (SRS WT-SRS-001…012, risk, security).

## Run

LibreDCM must have an admin token (`LIBRE_DCM_ADMIN_TOKEN` or `token_file`):
its control API refuses every request without one. Give Libre WT the same
token.

```sh
go build -o bin/LibreWT ./modules/wt/cmd/librewt
LIBRE_WT_DCM_URL=http://127.0.0.1:8081 LIBRE_WT_DCM_TOKEN_FILE=./dcm-admin-token ./bin/LibreWT
# or: ./bin/LibreWT -config modules/wt/config.example.yaml
```

Open http://127.0.0.1:8090 and create the first administrator. Serve HTTPS
(`tls_cert_file` / `tls_key_file`) before exposing Libre WT on a network.

Container: `docker build -f deploy/docker/Dockerfile.wt -t libre/wt .`, then
run with `-e LIBRE_WT_DCM_URL=... -e LIBRE_WT_DCM_TOKEN=... -v librewt:/var/lib/librewt -p 127.0.0.1:8090:8090`.

Not yet: fleet view, configuration, logs, service control, OIDC/LDAP, TOTP,
IHE IOCM rejection notes to other systems.
