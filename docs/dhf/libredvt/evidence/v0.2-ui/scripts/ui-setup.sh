#!/bin/bash
# Card DVT-0.2/5: configure the demo LibreDVT through its web forms (curl,
# CSRF token from the page) and run the three bundled example plans.
#   bash ui-setup-dvt05.sh OUTDIR
S=${DVT05_DIR:-${TMPDIR:-/tmp}/dvt05}
U=http://127.0.0.1:18095
OUT=${1:?outdir}
mkdir -p "$OUT"
J=$(mktemp "${TMPDIR:-/tmp}/dvt05-jar.XXXXXX")
PW=$(cat "$S/pki/admin-password")
c() { curl -s -b "$J" -c "$J" "$@"; }
c -o /dev/null -w "login %{http_code}\n" --data-urlencode "password=$PW" $U/login
CSRF=$(c $U/targets | grep -o 'name="csrf" value="[0-9a-f]*"' | head -1 | sed 's/.*value="//; s/"//')
[ -n "$CSRF" ] || { echo "no csrf"; exit 1; }
post() { c -o /dev/null -w "$1 %{http_code}\n" --data-urlencode "csrf=$CSRF" "${@:2}" $U/targets; }
post libredcm-tls-lab -d protocol=dimse -d name=libredcm-tls-lab -d class=lab -d host=libredcm -d port=11112 \
  -d called_aet=LIBREDCM -d calling_aet=LIBREDVT -d tls=on -d tls_ca_file=/pki/ca.crt
post dicomweb-lab -d protocol=dicomweb -d name=dicomweb-lab -d class=lab -d base_url=https://orthanc-https:8042/dicom-web \
  -d auth_type=bearer -d token_file=/pki/token -d tls_ca_file=/pki/ca.crt
post pacs-production -d protocol=dimse -d name=pacs-production -d class=production -d host=pacs.hospital.example -d port=104 \
  -d called_aet=PACS -d calling_aet=LIBREDVT
run() { # plan target label
  loc=$(c -o /dev/null -w "%{redirect_url}" -F "csrf=$CSRF" -F "bundled=$1" -F "target=$2" $U/plans/run)
  id=${loc##*/runs/}
  echo "run $3: $id"
  for _ in $(seq 1 120); do c $U/runs/$id | grep -q 'running…' || break; sleep 1; done
  for f in report.html junit.xml run.json plan.yaml; do c -o "$OUT/$3-$f" $U/runs/$id/$f; done
  python3 -c "import json,sys; r=json.load(open(sys.argv[1])); print('  verdict', r['verdict'], '|', '; '.join(s['id']+'='+(s.get('verdict') or s['status']) for s in r['steps'])); [print('   ', s['id'], m) for s in r['steps'] if (s.get('verdict') or s['status']) not in ('pass','passed') for m in s.get('messages',[])]" "$OUT/$3-run.json"
}
run libredcm-smoke-tls.yaml "" libredcm-smoke
run generic-dicom-tls-audit.yaml libredcm-tls-lab dicom-tls-audit
run generic-dicomweb-audit.yaml "" dicomweb-audit
c $U/targets > "$OUT/targets-page.html"
TOKEN=$(tr -d '\n' < "$S/pki/token")
echo "token hits in downloaded artefacts and pages: $(grep -l -F "$TOKEN" "$OUT"/* 2>/dev/null | wc -l) of $(ls "$OUT" | wc -l) files"
echo "token hits in the LibreDVT data directory: $(docker exec dvt05-libredvt grep -r -l -F "$TOKEN" /var/lib/libredvt 2>/dev/null | wc -l) files"
rm -f "$J"
