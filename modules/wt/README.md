# Libre WT (Watch Tower)

Web management console for LibreDCM, part of Libre RWS. It has:

- local accounts (argon2id), roles Viewer / Operator / Admin, first-run
  administrator creation, no default credentials;
- connections to one or more LibreDCM nodes ("instances") through their
  control API, each with its own admin token;
- **Fleet** page: every instance, by cluster label, with health, version,
  uptime, licence state, open associations, stored and not-stored counts,
  rejected associations, outbox depth and open exceptions;
- **Dashboard** and **Status** page per instance: identity, readiness checks,
  backends, counters per DICOM service, with the time of the reading;
- **About** page: the Libre WT version and the version, node ID and AE title
  of each instance;
- **Study Exceptions** per instance: images LibreDCM held because their
  Patient ID (or series/study) contradicts the archive. Admins correct the
  incoming images, correct the archived study, or reject the incoming images.
  Every edit shows exactly what will change and needs an explicit approval
  and a reason;
- **Configuration** per instance: the settings the node runs with, where
  each comes from and whether it changes without a restart; secrets show
  only as "set" or "not set";
- **Destinations** and **Allow-list**: the C-MOVE destinations and the
  calling-AE allow-list in force; an empty allow-list is shown as a
  warning (every calling AE may connect);
- **Edit, reload, rollback** (Admin): change the destinations or the
  allow-list, reload the configuration files, or put the previous
  console-made version back. Each shows LibreDCM's plan of exactly what
  changes first, with a box for each warning, and needs approval and a
  reason;
- **C-ECHO test**: from the node to a configured destination (Operator and
  Admin) or to another address (Admin), with the result or the stage that
  failed;
- audit log of logins, user changes, page openings of configuration pages,
  reviews, approvals, results and accepted instance identities, with a
  filter per instance.

All roles see the fleet, dashboard, status, about and Study Exceptions pages.
Operators also see the configuration, destinations and allow-list and run
C-ECHO tests to configured destinations. Only an Admin changes the
configuration, reloads, rolls back, tests a free address, resolves
exceptions, accepts a new instance identity, manages users and reads the
audit log.

Libre WT does not display images. It does not start, stop or restart
LibreDCM: that is done on the host (see `deploy/workstation/INSTALL.md`,
"Day-to-day").

Design file: `docs/dhf/librewt/` (SRS, risk, security). Status: draft, not
yet approved for clinical use.

## Run with one LibreDCM

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

The single LibreDCM is shown as the instance `default`. Addresses of Libre
WT 0.1 (`/exceptions`) still work: they lead to `/i/default/exceptions`.

Container: `docker build -f deploy/docker/Dockerfile.wt -t libre/wt .`, then
run with `-e LIBRE_WT_DCM_URL=... -e LIBRE_WT_DCM_TOKEN=... -v librewt:/var/lib/librewt -p 127.0.0.1:8090:8090`.

## Run with several LibreDCM nodes

List the nodes in the configuration file. The list can't be given by
environment variables.

```yaml
instances:
  - name: dcm-1                       # letters, digits, - and _; unique
    cluster: site-a                   # optional: same label = one group on the fleet page
    url: https://dcm-1.example:8081   # the admin listener of this node, not a load balancer
    token_file: ./tokens/dcm-1.token  # this node's admin token (or token: "...")
    ca_file: ./tls/dcm-1.crt          # optional: trust this node's certificate
  - name: dcm-2
    cluster: site-a
    url: https://dcm-2.example:8081
    token_file: ./tokens/dcm-2.token
```

Start with `./bin/LibreWT -config /path/to/config.yaml`. In a container,
mount the file and the token files and add `-config /etc/librewt/config.yaml`
after the image name.

Rules:

- Use `instances:` or the single `dcm:` block, not both. Libre WT does not
  start when both are set (also when `LIBRE_WT_DCM_TOKEN` or
  `LIBRE_WT_DCM_TOKEN_FILE` is set next to an `instances:` list).
- Libre WT does not start when two instances have the same name (upper and
  lower case count as the same), a name has other characters, or an instance
  has no token. At most 64 instances.
- Point each entry at one node's admin listener. Behind a load balancer the
  answers would come from changing nodes.
- Keep token files readable by the Libre WT service user only. Tokens never
  appear on a page, in the log or in the audit log.
- Use `https://` for a node on another machine. With `http://` to another
  machine, Libre WT warns on the instance's pages and in its log: the token
  and patient data would cross the network unencrypted.

## What the pages tell you

- **Every instance page names the instance.** The address is
  `/i/<name>/…` and a banner shows the name, AE title, node ID and cluster
  label. Libre WT keeps no "selected instance": two browser tabs on two
  instances do not affect each other.
- **Unreachable.** When a node does not answer, Libre WT shows
  "unreachable", the reason and the time of the last successful contact. It
  shows none of the node's earlier values.
- **Readings are a few seconds old at most.** Libre WT reads a node when a
  page is opened and reuses the reading for 5 seconds for all users
  (`status_cache`). It does not read in the background and the pages do not
  refresh on their own: reload the page for a new reading. A node that does
  not answer makes the page wait up to 5 seconds.
- **"Settings differ in cluster" / "version differs in cluster".** Instances
  with the same cluster label are compared: the settings that change without
  a restart (C-MOVE destinations, calling-AE allow-list) and the LibreDCM
  version. The flag is on the instance that differs from the most common
  value, or on all when there is no most common value.
- **"Another node answers at this instance's address".** Libre WT remembers
  the node ID and AE title each instance reported first. If they change,
  Libre WT warns on every page of that instance and sends no edit to it.
  Check the address and the node. If the change is intended (node replaced,
  AE title changed), an Admin opens "Review the new identity", gives a
  reason and approves. The acceptance is recorded in the audit log.
- **Older LibreDCM.** A LibreDCM without the newer control API shows "does
  not report status". Study Exceptions still work for it.
- **Licence.** The licence state shown never stops DICOM services. An
  unlicensed node that waits for the licence notice to be acknowledged
  refuses configuration changes (edit, reload, rollback); Libre WT says so.
  DICOM keeps running.

## Changing the configuration

Only the two settings LibreDCM can change without a restart can be changed
in Libre WT: the C-MOVE destinations and the calling-AE allow-list. All
other settings are read-only; change them on the host and restart the node.

1. Open the instance, then **Configuration** → **Edit destinations or
   allow-list**. Change the table or the rules and press **Review the
   change…**. Nothing changes yet.
2. Libre WT asks LibreDCM for its plan and shows it: what is added, removed
   or changed, on which instance (banner, node ID, AE title). Each warning
   that weakens a safety check (a destination removed or pointed elsewhere,
   an allow-list that becomes empty or would refuse a device used in the
   last 24 hours) has its own box.
3. Tick each warning, give a reason, tick the approval box and press
   **Approve and apply**. The approval is valid once, for 10 minutes, in
   this browser session, for this plan. If the node's settings changed in
   the meantime, LibreDCM refuses and nothing is changed: review again.

LibreDCM keeps the change in its own overlay file (`config.yaml` is not
rewritten) and keeps the version before it for one **rollback**. **Reload
from the files** applies what the files on the host hold now, also through
a plan page. In a cluster, each node is changed on its own: the fleet page
flags the nodes whose settings differ.

The result page shows the request ID. It is in the audit log of Libre WT
and of LibreDCM. If LibreDCM does not answer in time, Libre WT says that the
outcome is not known: look for the request ID before you try again.

## C-ECHO test

**C-ECHO test** on an instance makes that node open a DICOM association
under its own AE title and send one C-ECHO. It shows whether a device can be
reached and accepts the node, as for a C-MOVE. No patient data is sent.
Operators test configured destinations; an Admin can also test a host, port
and AE title that are not configured (for a new device), with or without
TLS (the device's certificate is not checked). A failure shows the stage:
**connect** (network or TLS), **association** (the device refused the
node's AE title or the negotiation) or **echo**.

## Upgrade from Libre WT 0.1

Replace the program or image; keep the data directory. `librewt.db` is
upgraded in place at the first start (the audit log gains the instance; a
table for instance identities is added). Bookmarks to `/exceptions…` keep
working while one instance is configured. After login the first page is now
the fleet page. Going back to 0.1 with the upgraded database works; 0.1
ignores the additions.

Not yet: study search and send, licence handling, backup, LibreDCM audit
records and logs (later cards of 0.2); OIDC/LDAP, TOTP, IHE IOCM rejection
notes to other systems.
