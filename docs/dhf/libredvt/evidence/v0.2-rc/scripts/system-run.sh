#!/bin/bash
# LibreDVT v0.2 card DVT-0.2/6: release-candidate system run.
#   1. The release-candidate image (libre/dvt:0.2.0-rc.1) runs the bundled
#      plans from the command line against real systems: LibreDCM in TLS mode
#      (LibreDCM smoke, Generic DICOM TLS audit), LibreDCM in plain mode (the
#      v0.1 LibreDCM conformance plan, unchanged) and Orthanc + DICOMweb over
#      HTTPS with a bearer token (Generic DICOMweb audit).
#   2. Cross-check of the Generic DICOM TLS audit against DCMTK 3.6.7
#      (DVT-SRS-156): the plan runs against DCMTK storescp in four set-ups
#      (+tls, plain, retired AES profile that still accepts TLS 1.0/1.1, a
#      certificate for another host), and DCMTK's own echoscu and server
#      logs state the same facts independently.
#   3. The anonymised sample audit report: the Generic DICOM TLS audit
#      against a LibreDCM lab container that answers as "pacs.lab.example"
#      with AE title LABPACS (no client, host or person names).
# Certificates come from the openssl command line on the host (synthetic
# CAs), so neither Go's nor Python's TLS code made them.
#
#   bash docs/dhf/libredvt/evidence/v0.2-rc/scripts/system-run.sh   # from the repository root
cd "$(git rev-parse --show-toplevel)" || exit 1
E=docs/dhf/libredvt/evidence/v0.2-rc
OUT=$E/system-run
DVT_IMG=${DVT_IMG:-libre/dvt:0.2.0-rc.1}
DCM_IMG=${DCM_IMG:-libre/dcm:dvt-0.2-rc}
DCMTK_IMG=${DCMTK_IMG:-rws-e2e-dcmtk}
ORTHANC_IMG=orthancteam/orthanc:26.11.1@sha256:25738bcd515926ac8d33cacf09729a410a32c0a65d6cb8e61bd86fce81a4cd30
NET=dvt06-net
PKI=$(mktemp -d "${TMPDIR:-/tmp}/dvt06-pki.XXXXXX")
mkdir -p "$OUT"; chmod 777 "$OUT"
echo "commit $(git rev-parse --short HEAD), $(date -u +%Y-%m-%dT%H:%M:%SZ)"
for i in "$DVT_IMG" "$DCM_IMG" "$DCMTK_IMG" "$ORTHANC_IMG"; do echo "image $i $(docker inspect -f '{{.Id}}' "$i")"; done
echo "libredvt version: $(docker run --rm "$DVT_IMG" version 2>&1 | head -3 | paste -sd' ')"

echo "== synthetic PKI (openssl $(openssl version | cut -d' ' -f2))"
cd "$PKI" || exit 1
ca() { # name subject
  openssl req -x509 -newkey rsa:2048 -nodes -keyout "$1.key" -out "$1.crt" -days 3 -subj "$2" \
    -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" 2>/dev/null
}
leaf() { # name subject SAN eku ca
  openssl req -newkey rsa:2048 -nodes -keyout "$1.key" -out "$1.csr" -subj "$2" 2>/dev/null
  printf 'subjectAltName=%s\nextendedKeyUsage=%s\nbasicConstraints=CA:FALSE\n' "$3" "$4" > "$1.ext"
  openssl x509 -req -in "$1.csr" -CA "$5.crt" -CAkey "$5.key" -CAcreateserial -days 2 -extfile "$1.ext" -out "$1.crt" 2>/dev/null
}
ca ca "/O=LibreDVT RC verification/CN=dvt06-test-ca"
ca sample-ca "/O=Example Clinic Lab/CN=Example Clinic Lab CA"
leaf libredcm "/O=LibreDVT RC verification/CN=libredcm" "DNS:libredcm" serverAuth ca
leaf dcmtk-peer "/O=LibreDVT RC verification/CN=dcmtk-peer" "DNS:dcmtk-tls,DNS:dcmtk-aes" serverAuth,clientAuth ca
leaf wrong-host "/O=LibreDVT RC verification/CN=other" "DNS:other.test" serverAuth ca
leaf orthanc "/O=LibreDVT RC verification/CN=orthanc-https" "DNS:orthanc-https" serverAuth ca
leaf sample-pacs "/O=Example Clinic Lab/CN=pacs.lab.example" "DNS:pacs.lab.example" serverAuth sample-ca
cat orthanc.crt orthanc.key > orthanc.pem
printf 'dvt06-synthetic-token-%s\n' "$(openssl rand -hex 16)" > token
rm -f ./*.csr ./*.ext ./*.srl
chmod 755 "$PKI"; chmod 644 "$PKI"/*
for f in libredcm dcmtk-peer wrong-host orthanc sample-pacs; do echo "$f: $(openssl x509 -in $f.crt -noout -subject -ext subjectAltName | tr '\n' ' ')"; done
cd - >/dev/null || exit 1

cleanup() {
  docker rm -f dvt06-libredcm-tls dvt06-libredcm-plain dvt06-sample-pacs dvt06-dcmtk-tls dvt06-dcmtk-plain dvt06-dcmtk-aes \
    dvt06-dcmtk-wrong dvt06-orthanc >/dev/null 2>&1
  docker network rm $NET >/dev/null 2>&1
}
cleanup
trap cleanup EXIT
docker network create $NET >/dev/null
dcm() { # name alias extra-env...
  local n=$1 a=$2; shift 2
  docker run -d --name "$n" --network $NET --network-alias "$a" -v "$PKI":/pki:ro -e LIBRE_DCM_LOG_FORMAT=text "$@" "$DCM_IMG" >/dev/null
}
dcm dvt06-libredcm-tls libredcm -e LIBRE_DCM_TLS_CERT=/pki/libredcm.crt -e LIBRE_DCM_TLS_KEY=/pki/libredcm.key
dcm dvt06-libredcm-plain libredcm-plain -e LIBRE_DCM_REMOTE_AES=LIBREDVT=dvt-runner:11115
dcm dvt06-sample-pacs pacs.lab.example -e LIBRE_DCM_TLS_CERT=/pki/sample-pacs.crt -e LIBRE_DCM_TLS_KEY=/pki/sample-pacs.key -e LIBRE_DCM_AE_TITLE=LABPACS
scp() { # name alias args...
  local n=$1 a=$2; shift 2
  docker run -d --name "$n" --network $NET --network-alias "$a" -v "$PKI":/pki:ro --entrypoint storescp "$DCMTK_IMG" -d "$@" -od /tmp 11112 >/dev/null
}
scp dvt06-dcmtk-tls dcmtk-tls +tls /pki/dcmtk-peer.key /pki/dcmtk-peer.crt +cf /pki/ca.crt -ic
scp dvt06-dcmtk-plain dcmtk-plain
scp dvt06-dcmtk-aes dcmtk-aes +tls /pki/dcmtk-peer.key /pki/dcmtk-peer.crt +cf /pki/ca.crt -ic +pa
scp dvt06-dcmtk-wrong dcmtk-wrong +tls /pki/wrong-host.key /pki/wrong-host.crt +cf /pki/ca.crt -ic
docker run -d --name dvt06-orthanc --network $NET --network-alias orthanc-https -v "$PKI":/pki:ro \
  -e DICOM_WEB_PLUGIN_ENABLED=true -e ORTHANC__AUTHENTICATION_ENABLED=false -e ORTHANC__NAME=orthanc-https \
  -e ORTHANC__SSL_ENABLED=true -e ORTHANC__SSL_CERTIFICATE=/pki/orthanc.pem -e ORTHANC__SSL_MINIMUM_PROTOCOL_VERSION=4 -e ORTHANC__DICOM_WEB__SSL=true \
  "$ORTHANC_IMG" >/dev/null
for _ in $(seq 1 60); do docker logs dvt06-orthanc 2>&1 | grep -q "Orthanc has started" && break; sleep 1; done
for c in dvt06-libredcm-tls dvt06-libredcm-plain dvt06-sample-pacs; do
  for _ in $(seq 1 40); do [ "$(docker inspect -f '{{.State.Health.Status}}' $c 2>/dev/null)" = healthy ] && break; sleep 1; done
  echo "$c health: $(docker inspect -f '{{.State.Health.Status}}' $c)"
done
echo "orthanc: $(docker logs dvt06-orthanc 2>&1 | grep -c 'Orthanc has started') start line(s)"
sleep 1

cat > "$OUT/targets.yaml" <<'EOF'
targets:
  - {name: libredcm-tls-lab, class: lab, protocol: dimse, host: libredcm, port: 11112, called_aet: LIBREDCM, calling_aet: LIBREDVT, tls: {ca_file: /pki/ca.crt}}
  - {name: libredcm-lab, class: lab, protocol: dimse, host: libredcm-plain, port: 11112, called_aet: LIBREDCM, calling_aet: LIBREDVT}
  - {name: dcmtk-tls, class: lab, protocol: dimse, host: dcmtk-tls, port: 11112, called_aet: STORESCP, tls: {ca_file: /pki/ca.crt}}
  - {name: dcmtk-plain, class: lab, protocol: dimse, host: dcmtk-plain, port: 11112, called_aet: STORESCP, tls: {ca_file: /pki/ca.crt}}
  - {name: dcmtk-aes, class: lab, protocol: dimse, host: dcmtk-aes, port: 11112, called_aet: STORESCP, tls: {ca_file: /pki/ca.crt}}
  - {name: dcmtk-wrong-host, class: lab, protocol: dimse, host: dcmtk-wrong, port: 11112, called_aet: STORESCP, tls: {ca_file: /pki/ca.crt}}
  - name: dicomweb-lab
    class: lab
    protocol: dicomweb
    base_url: https://orthanc-https:8042/dicom-web
    auth: {type: bearer, token_file: /pki/token}
    tls: {ca_file: /pki/ca.crt}
  - name: example-clinic-lab-pacs
    class: lab
    protocol: dimse
    host: pacs.lab.example
    port: 11112
    called_aet: LABPACS
    calling_aet: AUDITOR
    tls: {ca_file: /pki/sample-ca.crt}
EOF
chmod 644 "$OUT/targets.yaml"

P=/opt/libredvt/plans
run() { # label target plan
  docker run --rm --name dvt-runner --network $NET -v "$PKI":/pki:ro -v "$PWD/$OUT":/out "$DVT_IMG" run -targets /out/targets.yaml -target "$2" \
    -junit "/out/$1-junit.xml" -html "/out/$1-report.html" -json "/out/$1-run.json" "$P/$3" > "$OUT/$1.log" 2>&1
  local rc=$?
  echo "$1 (target $2, plan $3): exit $rc :: $(tail -1 "$OUT/$1.log")"
}
echo "== 1. release candidate against real systems"
run rc-libredcm-smoke libredcm-tls-lab libredcm-smoke-tls.yaml
run rc-dicom-tls-audit-libredcm libredcm-tls-lab generic-dicom-tls-audit.yaml
run rc-libredcm-conformance-v01 libredcm-lab libredcm-conformance.yaml
run rc-dicomweb-audit dicomweb-lab generic-dicomweb-audit.yaml

echo "== 2. Generic DICOM TLS audit against DCMTK storescp"
run xc-audit-dcmtk-tls dcmtk-tls generic-dicom-tls-audit.yaml
run xc-audit-dcmtk-plain dcmtk-plain generic-dicom-tls-audit.yaml
run xc-audit-dcmtk-aes dcmtk-aes generic-dicom-tls-audit.yaml
run xc-audit-dcmtk-wrong-host dcmtk-wrong-host generic-dicom-tls-audit.yaml
echo "-- DCMTK echoscu against the same servers (DCMTK does not check host names)"
echoscu() { # label args...
  local l=$1; shift
  docker run --rm --network $NET -v "$PKI":/pki:ro --entrypoint echoscu "$DCMTK_IMG" -v "$@" > "$OUT/dcmtk-echoscu-$l.log" 2>&1
  local rc=$?
  echo "exit $rc" >> "$OUT/dcmtk-echoscu-$l.log"
  echo "echoscu $l: exit $rc"
}
for s in libredcm dcmtk-tls dcmtk-plain dcmtk-aes dcmtk-wrong; do
  aec=STORESCP; [ $s = libredcm ] && aec=LIBREDCM
  echoscu "$s-tls" +tls /pki/dcmtk-peer.key /pki/dcmtk-peer.crt +cf /pki/ca.crt -aec $aec $s 11112
  echoscu "$s-plain" -aec $aec $s 11112
done
echoscu dcmtk-aes-tls-aes-profile +tls /pki/dcmtk-peer.key /pki/dcmtk-peer.crt +cf /pki/ca.crt +pa -aec STORESCP dcmtk-aes 11112

echo "== 3. anonymised sample audit"
run sample-dicom-tls-audit example-clinic-lab-pacs generic-dicom-tls-audit.yaml

echo "== server logs"
for c in dvt06-libredcm-tls dvt06-libredcm-plain dvt06-sample-pacs dvt06-dcmtk-tls dvt06-dcmtk-plain dvt06-dcmtk-aes dvt06-dcmtk-wrong dvt06-orthanc; do
  docker logs "$c" > "$OUT/server-${c#dvt06-}.log" 2>&1
done
echo "== TLS versions and suites each DCMTK server recorded (its own log, independent of LibreDVT)"
for c in dcmtk-tls dcmtk-plain dcmtk-aes dcmtk-wrong; do
  echo "$c: $(grep -E 'Protocol *:|Ciphersuite *:|TLS error|Association Received|Refusing|Aborted|Association Request Failed|PDU' "$OUT/server-$c.log" | sed -E 's/^[DEIW]: *//; s/ +/ /g; s/0x[0-9a-f]+//g' | sort | uniq -c | sed 's/^ *//' | paste -sd';' -)"
done
echo "orthanc stored instances (REST): $(docker exec dvt06-orthanc curl -sk https://localhost:8042/statistics 2>/dev/null | tr -d '\n ' )"
python3 $E/scripts/summarize.py "$OUT" "$PKI"
echo "summary exit $?"
