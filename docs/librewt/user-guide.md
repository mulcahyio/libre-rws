# Libre WT 0.2 user guide

**DRAFT for approval.** Written by Hermes (Claude) for human review,
2026-11-17. It describes Libre WT 0.2.0-rc.1. The screenshots come from the
system test (`scripts/e2e/wt-system-test.sh`) with synthetic data only;
patient names such as `WTSYS^ALPHA` are made up. Each screenshot has a link
to the same page in the dark colour scheme. Libre WT follows the colour
scheme of your browser or operating system.

Install and start-up: [`modules/wt/README.md`](../../modules/wt/README.md)
and [`deploy/workstation/INSTALL.md`](../../deploy/workstation/INSTALL.md).

## Contents

- [What Libre WT is](#what-libre-wt-is)
- [Roles](#roles)
- [First run and log in](#first-run-and-log-in)
- [How every change works](#how-every-change-works)
- [Fleet and about](#fleet-and-about)
- [Instance pages](#instance-pages)
- [Dashboard and status](#dashboard-and-status)
- [Study Exceptions](#study-exceptions)
- [Configuration](#configuration)
- [Change destinations or the allow-list](#change-destinations-or-the-allow-list)
- [Reload and roll back](#reload-and-roll-back)
- [C-ECHO test](#c-echo-test)
- [Licence](#licence)
- [Backup](#backup)
- [Studies and send](#studies-and-send)
- [LibreDCM audit records and logs](#libredcm-audit-records-and-logs)
- [Libre WT audit log, users and account](#libre-wt-audit-log-users-and-account)
- [When something goes wrong](#when-something-goes-wrong)

## What Libre WT is

Libre WT (Watch Tower) is the web console of Libre RWS. It watches and
manages one or more LibreDCM nodes. It talks to each node through the
node's control API, with that node's admin token.

- Libre WT is **not a diagnostic viewer**. It shows no images. Use a
  cleared viewer to read studies.
- Libre WT does not start, stop or restart LibreDCM. That is done on the
  host (`LibreDCM.sh` / `LibreDCM.ps1`, or Docker). The status page says so.
- The licence state never stops DICOM services. LibreDCM keeps storing,
  finding and sending images in every licence state.

## Roles

Each account has one role. The server checks the role on every request.
A page shows no button that your role can't use.

| What | Viewer | Operator | Admin |
|---|---|---|---|
| Fleet, dashboard, status, licence state, about | yes | yes | yes |
| Study Exceptions (view) | yes | yes | yes |
| Configuration, destinations, allow-list (view) | | yes | yes |
| Logs of a node | | yes | yes |
| Study search, study and series pages | | yes | yes |
| C-ECHO test to a configured destination | | yes | yes |
| Index backup | | yes | yes |
| Resolve Study Exceptions | | | yes |
| Change destinations or allow-list, reload, roll back | | | yes |
| Send a study | | | yes |
| Licence actions | | | yes |
| C-ECHO test to any address | | | yes |
| LibreDCM audit records, CSV export | | | yes |
| Accept a new node identity | | | yes |
| Users, Libre WT audit log | | | yes |

## First run and log in

Libre WT ships without accounts. On the first start, every page leads to
the first-run page. Create the first Admin there: a user name and a
password of at least 12 characters that is not the user name. After that
the first-run page no longer works.

Log in with your user name and password. After 5 failed attempts for one
user name, or 10 from one address, in 15 minutes, logins are refused for a
while. A session ends after 30 minutes without use and after 12 hours at
most.

![Login page](images/login-light.webp) ([dark](images/login-dark.webp))

## How every change works

Every action that changes something on a node follows the same steps.
Nothing changes before the last step.

1. You press the button of the action (for example **Review the change…**).
2. Libre WT asks LibreDCM for a **plan**: exactly what would change. A
   **warning page** shows the plan, the instance banner and LibreDCM's
   warnings.
3. You tick **every warning box** LibreDCM asks for, write a **reason**, and
   tick the **approval box**.
4. You press the button. Libre WT writes the approval to its audit log
   first, then sends the action with your user name, the reason and the
   plan. LibreDCM checks that nothing changed since the plan; if something
   did, it refuses and nothing is changed.

The approval is valid once, for 10 minutes, for this plan, this instance
and your session. A second press of the same button is refused.

![Warning page of a configuration change](images/config-review-light.webp) ([dark](images/config-review-dark.webp))

## Fleet and about

The **Fleet** page lists every configured node, grouped by cluster label,
with health, version, uptime, licence state, open associations, stored and
not-stored images, rejected associations, pending events and open
exceptions. A node that can't be reached shows no values, only the time of
the last contact. Within a cluster label, Libre WT flags nodes whose live
settings or versions differ ("settings differ").

![Fleet page](images/fleet-light.webp) ([dark](images/fleet-dark.webp))

The **About** page shows the Libre WT version and the version, node ID and
AE title of each node.

![About page](images/about-light.webp) ([dark](images/about-dark.webp))

## Instance pages

Every page of one node carries the node name in its address
(`/i/dcm-1/…`) and a banner: instance name, AE title, node ID, cluster and
whether it is reachable. Check the banner before every action. The tabs
below the banner show the pages your role may open.

Two warnings can appear in the banner:

- **plain HTTP**: Libre WT reaches this node without encryption over the
  network. Set an `https://` address and a CA file in the Libre WT
  configuration.
- **identity changed**: the node now reports another node ID or AE title
  than the one Libre WT remembered. No change is sent to it until an Admin
  checks it and accepts the new identity with a reason.

## Dashboard and status

The **Dashboard** shows the main values of the node and which control
functions it supports. The **Status** page shows identity, backends, every
readiness check, the licence state and counters per DICOM service, with
the time the values were read. It also tells you how to restart the node
on the host.

![Dashboard](images/dashboard-light.webp) ([dark](images/dashboard-dark.webp))

![Status page](images/status-light.webp) ([dark](images/status-dark.webp))

## Study Exceptions

When images arrive whose Patient ID contradicts the archive, LibreDCM keeps
them apart and hidden (patient-conflict policy `quarantine`). The **Study
Exceptions** page lists them. The detail page shows the archived and the
incoming patient side by side, with differences highlighted.

![Study Exceptions](images/exceptions-light.webp) ([dark](images/exceptions-dark.webp))

![Exception detail](images/exception-light.webp) ([dark](images/exception-dark.webp))

An Admin chooses one of three edits: correct the incoming images, correct
the archived study, or reject the incoming images. The warning page shows
exactly which files and values will change.

![Warning page of an exception edit](images/exception-review-light.webp) ([dark](images/exception-review-dark.webp))

## Configuration

The **Configuration** page shows every setting the node runs with, where it
comes from (file, environment, console overlay, default) and whether it
can change without a restart. Secrets (tokens, S3 keys, passwords) show
only as "set" or "not set". Settings that need a restart are read-only
here; change them on the host.

![Configuration](images/config-light.webp) ([dark](images/config-dark.webp))

The **Destinations** page lists the C-MOVE destinations in force (a send
from Libre WT can go only to these). The **Allow-list** page lists the
calling AEs that may connect. An empty allow-list is shown as a warning:
every calling AE may connect.

![Destinations](images/remote-aes-light.webp) ([dark](images/remote-aes-dark.webp))

![Allow-list](images/allow-list-light.webp) ([dark](images/allow-list-dark.webp))

## Change destinations or the allow-list

Admins only. Open **Configuration → Edit destinations or allow-list**
(or **Edit destinations…** / **Edit the allow-list…** on those pages). Destinations: change host,
port or AE title of a row, tick **remove**, or fill the empty row to add
one. Allow-list: one rule per line (`AE`, `AE@IP` or
`AE@network/prefix`). Then press **Review the change…** and follow
[the steps above](#how-every-change-works). The warning page lists each
added, removed and changed entry.

A device that is not on the allow-list can no longer store or retrieve
images. Before you narrow the allow-list, make sure every modality and
viewer is on it.

![Edit page](images/config-edit-light.webp) ([dark](images/config-edit-dark.webp))

## Reload and roll back

**Reload** makes the node read its configuration files again. **Roll back**
puts the previous console-made version back. Both show a plan page with the
difference first. On an unlicensed official build, LibreDCM blocks reloads
until an Admin acknowledges the unlicensed notice (see [Licence](#licence)).

![Reload plan](images/reload-review-light.webp) ([dark](images/reload-review-dark.webp))

![Rollback plan](images/rollback-review-light.webp) ([dark](images/rollback-review-dark.webp))

## C-ECHO test

Checks that a destination answers. Operators test configured destinations;
Admins can also test any host, port and AE title. The result shows success
or the stage that failed (connect, association, echo) and the time taken.

![C-ECHO test](images/echo-light.webp) ([dark](images/echo-dark.webp))

![C-ECHO warning page](images/echo-review-light.webp) ([dark](images/echo-review-dark.webp))

![C-ECHO result](images/echo-result-light.webp) ([dark](images/echo-result-dark.webp))

## Licence

Everyone sees the licence state, days left, expiry, customer, site, term,
machine fingerprint and acknowledgement. A source build shows "not
enforced". Licence keys show only their last four characters.

![Licence of a source build](images/license-light.webp) ([dark](images/license-dark.webp))

![Licence of an unlicensed official build](images/license-unlicensed-light.webp) ([dark](images/license-unlicensed-dark.webp))

Admins can:

- **acknowledge** the unlicensed notice. You type your own user name to
  sign. This lifts the block on configuration reloads for 24 hours;
- create an **activation request** file (for offline activation). It is
  downloaded as a file;
- **install** a licence file (text, at most 128 KiB);
- **activate online** with a licence key.

A licence LibreDCM refuses is reported, and the installed licence stays.

![Acknowledgement warning page](images/license-ack-review-light.webp) ([dark](images/license-ack-review-dark.webp))

## Backup

Operators and Admins can start an online backup of the node's index. You
can't choose a path or file name, and Libre WT offers no download or
delete. The result shows the file name, size and SHA-256 checksum. The
backup page lists the backups on the node and the backups started from
Libre WT (who, when, why).

**The backup holds the index only.** Back up the image store separately.

![Backups](images/backups-light.webp) ([dark](images/backups-dark.webp))

![Backup warning page](images/backup-review-light.webp) ([dark](images/backup-review-dark.webp))

![Backup result](images/backup-result-light.webp) ([dark](images/backup-result-dark.webp))

## Studies and send

Operators and Admins can search the studies of a node by Patient ID or
name (with `*` and `?`), study date, accession number, modality or Study
Instance UID. A search needs at least one value; `*` alone does not count.
Every search and every study or series page is written to the Libre WT
audit log first, under your name.

![Study search](images/studies-light.webp) ([dark](images/studies-dark.webp))

![Search result](images/studies-search-light.webp) ([dark](images/studies-search-dark.webp))

![Study page](images/study-light.webp) ([dark](images/study-dark.webp))

![Series page](images/series-light.webp) ([dark](images/series-dark.webp))

**Send** (Admin): on the study page, choose a configured destination and,
if you want, some series (none ticked means the whole study). The warning
page shows the patient's name, ID and birth date three times, the study,
the number of series and instances, and the destination. **Check the
patient before you send.** Images that reach the wrong device can't be
called back. The result shows complete, partial or failed, with every
instance that failed.

![Send warning page](images/send-review-light.webp) ([dark](images/send-review-dark.webp))

![Send result](images/send-result-light.webp) ([dark](images/send-result-dark.webp))

## LibreDCM audit records and logs

**LibreDCM audit** (Admin) shows the node's recent audit records; **Logs**
(Operator, Admin) its recent log lines. Both show only what the node keeps
in memory; the site's log collector holds the full record. Filters: event
type, outcome or level, period, number. Admins can export what is shown as
a CSV file; the export is audited.

![LibreDCM audit records](images/dcm-audit-light.webp) ([dark](images/dcm-audit-dark.webp))

![Logs](images/logs-light.webp) ([dark](images/logs-dark.webp))

## Libre WT audit log, users and account

The **Audit log** (Admin) lists what people did in Libre WT: logins,
user changes, page openings with patient data, reviews, approvals and
results, by instance.

![Libre WT audit log](images/wt-audit-light.webp) ([dark](images/wt-audit-dark.webp))

**Users** (Admin): add a user with a role, change roles, disable or enable,
reset a password. The last active Admin can't be disabled or demoted.
**Account**: change your own password.

![Users](images/users-light.webp) ([dark](images/users-dark.webp))

![Account](images/account-light.webp) ([dark](images/account-dark.webp))

## When something goes wrong

| You see | What it means | What to do |
|---|---|---|
| "This instance can't be reached" | Libre WT gets no answer from the node | Check the node on the host; check the address and token in the Libre WT configuration |
| HTTP 401 on an instance | The token in Libre WT does not match the node | Put the node's current admin token in the token file |
| "identity changed" banner | Another node answers at this address, or its AE title changed | Check on the host; an Admin accepts the new identity with a reason |
| "Nothing was changed" after an approval | The node's state changed after the warning page (LibreDCM refused) | Start the action again and review the new plan |
| "outcome not known" | No answer within the time limit (110 s for sends and backups) | Look up the request ID in the LibreDCM audit records before you repeat it |
| Reload refused, link to the licence page | Unlicensed official build, notice not acknowledged | An Admin acknowledges the notice on the licence page |
