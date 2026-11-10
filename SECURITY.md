# Security policy

Libre RWS (including LibreDCM) handles medical images and patient data. We
follow coordinated vulnerability disclosure, as required for medical device
software (FD&C Act §524B, FDA cybersecurity guidance).

> **DRAFT**: response times and contact details are pending approval.

## Reporting a vulnerability

- Use GitHub's private reporting: **Security → Report a vulnerability** on
  this repository, or email **security@mulcahy.io** (to be set up; until then
  dev@mulcahy.io).
- Please do not open a public issue, and do not test against systems that
  hold real patient data.
- Include the version or image digest, the affected component, steps to
  reproduce and the impact you see.

## What happens next

| Step | Target |
|---|---|
| Acknowledge your report | 3 business days |
| Initial assessment (severity, CVSS, patient-safety impact) | 10 business days |
| Fix or mitigation for critical/high issues | 30 days, sooner if exploited in the wild |
| Fix for medium/low issues | next scheduled release |
| Public advisory and CVE (with credit, if you want it) | when a fix or mitigation is available |

Customers are told about vulnerabilities that affect them, with the fix or a
mitigation, through release notes and security advisories.

## How we look for vulnerabilities

Every change is scanned (gosec, govulncheck), the default branch is
re-scanned weekly against new advisories, parsers are fuzzed weekly, and
each release ships an SBOM. The full process is
[QMS-SOP-010](docs/qms/sop-010-vulnerability-management.md).

## Supported versions

Only the latest release receives security fixes during the beta period.
A support and end-of-support policy will be published with the first general
release.

## Software bill of materials

Each release ships a CycloneDX SBOM (`sbom.cdx.json`) listing all third-party
components.
