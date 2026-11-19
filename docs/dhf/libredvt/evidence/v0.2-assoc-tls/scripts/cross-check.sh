#!/bin/bash
# LibreDVT v0.2 card DVT-0.2/3: association and TLS cross-check against
# independent implementations.
#   - DCMTK 3.6.7 storescp (OFFIS, OpenSSL) as association and TLS peer:
#     plaintext, TLS without client authentication, mutual TLS, wrong host
#     name, the AES profile (old TLS versions), and --refuse.
#   - LibreDCM in TLS mode (Go crypto/tls listener).
#   - DCMTK echoscu +tls against the same servers, so both tools are seen
#     side by side.
# Certificates come from the openssl command line on the host (RSA, a
# synthetic test CA), so neither Go's nor Python's TLS code made them.
#
#   bash docs/dhf/libredvt/evidence/v0.2-assoc-tls/scripts/cross-check.sh   # from the repository root
# Needs: libre/dvt:v02-assoc-tls (this branch), libre/dcm:v02-foundation
# (LibreDCM; modules/dicom unchanged on this branch), rws-e2e-dcmtk (DCMTK).
cd "$(git rev-parse --show-toplevel)" || exit 1
E=docs/dhf/libredvt/evidence/v0.2-assoc-tls
OUT=$E/cross-check
DVT_IMG=${DVT_IMG:-libre/dvt:v02-assoc-tls}
DCM_IMG=${DCM_IMG:-libre/dcm:v02-foundation}
DCMTK_IMG=${DCMTK_IMG:-rws-e2e-dcmtk}
NET=dvt03-net
PKI=$(mktemp -d "${TMPDIR:-/tmp}/dvt03-pki.XXXXXX")
mkdir -p "$OUT"; chmod 777 "$OUT"
echo "commit $(git rev-parse --short HEAD), $(date -u +%Y-%m-%dT%H:%M:%SZ)"
for i in "$DVT_IMG" "$DCM_IMG" "$DCMTK_IMG"; do echo "image $i $(docker inspect -f '{{.Id}}' "$i")"; done

echo "== synthetic PKI (openssl $(openssl version | cut -d' ' -f2))"
cd "$PKI" || exit 1
openssl req -x509 -newkey rsa:2048 -nodes -keyout ca.key -out ca.crt -days 2 -subj "/O=LibreDVT cross-check/CN=dvt03-test-ca" \
  -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" 2>/dev/null
openssl req -x509 -newkey rsa:2048 -nodes -keyout other-ca.key -out other-ca.crt -days 2 -subj "/CN=unrelated-ca" \
  -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" 2>/dev/null
leaf() { # name CN SAN ext ca
  openssl req -newkey rsa:2048 -nodes -keyout "$1.key" -out "$1.csr" -subj "/O=LibreDVT cross-check/CN=$2" 2>/dev/null
  printf 'subjectAltName=%s\nextendedKeyUsage=%s\nbasicConstraints=CA:FALSE\n' "$3" "$4" > "$1.ext"
  openssl x509 -req -in "$1.csr" -CA "${5:-ca}.crt" -CAkey "${5:-ca}.key" -CAcreateserial -days 1 -extfile "$1.ext" -out "$1.crt" 2>/dev/null
}
leaf dcmtk-peer dcmtk-peer "DNS:dcmtk-tls,DNS:dcmtk-mtls,DNS:dcmtk-aes" serverAuth,clientAuth
leaf wrong-host other "DNS:other.test" serverAuth
leaf libredcm libredcm "DNS:libredcm" serverAuth
leaf libredvt-client libredvt-client "DNS:libredvt-client" clientAuth
leaf stranger stranger "DNS:stranger" clientAuth other-ca
rm -f ./*.csr ./*.ext ./*.srl
chmod 755 "$PKI"; chmod 644 "$PKI"/*
for f in dcmtk-peer wrong-host libredcm libredvt-client; do echo "$f: $(openssl x509 -in $f.crt -noout -subject -ext subjectAltName | tr '\n' ' ')"; done
cd - >/dev/null || exit 1

docker network rm $NET >/dev/null 2>&1; docker network create $NET >/dev/null
cleanup() { docker rm -f dcmtk-plain dcmtk-refuse dcmtk-tls dcmtk-mtls dcmtk-wrong dcmtk-aes libredcm >/dev/null 2>&1; docker network rm $NET >/dev/null 2>&1; docker volume rm dvt03-plans >/dev/null 2>&1; }
trap cleanup EXIT
scp() { # name args...
  local n=$1; shift
  docker run -d --name "$n" --network $NET -v "$PKI":/pki:ro --entrypoint storescp "$DCMTK_IMG" -d "$@" -od /tmp 11112 >/dev/null
}
scp dcmtk-plain
scp dcmtk-refuse --refuse
scp dcmtk-tls +tls /pki/dcmtk-peer.key /pki/dcmtk-peer.crt +cf /pki/ca.crt -ic
scp dcmtk-mtls +tls /pki/dcmtk-peer.key /pki/dcmtk-peer.crt +cf /pki/ca.crt -rc
scp dcmtk-wrong +tls /pki/wrong-host.key /pki/wrong-host.crt +cf /pki/ca.crt -ic
scp dcmtk-aes +tls /pki/dcmtk-peer.key /pki/dcmtk-peer.crt +cf /pki/ca.crt -ic +pa
docker run -d --name libredcm --network $NET -v "$PKI":/pki:ro -e LIBRE_DCM_TLS_CERT=/pki/libredcm.crt -e LIBRE_DCM_TLS_KEY=/pki/libredcm.key \
  -e LIBRE_DCM_LOG_FORMAT=text "$DCM_IMG" >/dev/null
for _ in $(seq 1 40); do [ "$(docker inspect -f '{{.State.Health.Status}}' libredcm 2>/dev/null)" = healthy ] && break; sleep 1; done
echo "LibreDCM health: $(docker inspect -f '{{.State.Health.Status}}' libredcm)"
sleep 1

cat > "$OUT/targets.yaml" <<'EOF'
targets:
  - {name: dcmtk-plain, class: lab, protocol: dimse, host: dcmtk-plain, port: 11112, called_aet: STORESCP}
  - {name: dcmtk-refuse, class: lab, protocol: dimse, host: dcmtk-refuse, port: 11112, called_aet: STORESCP}
  - name: dcmtk-tls
    class: lab
    host: dcmtk-tls
    port: 11112
    called_aet: STORESCP
    tls: {ca_file: /pki/ca.crt, client_cert_file: /pki/libredvt-client.crt, client_key_file: /pki/libredvt-client.key}
  - name: dcmtk-mtls
    class: lab
    host: dcmtk-mtls
    port: 11112
    called_aet: STORESCP
    tls: {ca_file: /pki/ca.crt, client_cert_file: /pki/libredvt-client.crt, client_key_file: /pki/libredvt-client.key}
  - name: dcmtk-mtls-stranger
    class: lab
    host: dcmtk-mtls
    port: 11112
    called_aet: STORESCP
    tls: {ca_file: /pki/ca.crt, client_cert_file: /pki/stranger.crt, client_key_file: /pki/stranger.key}
  - {name: dcmtk-wrong, class: lab, host: dcmtk-wrong, port: 11112, called_aet: STORESCP, tls: {ca_file: /pki/ca.crt}}
  - {name: dcmtk-aes, class: lab, host: dcmtk-aes, port: 11112, called_aet: STORESCP, tls: {ca_file: /pki/ca.crt}}
  - {name: libredcm, class: lab, host: libredcm, port: 11112, called_aet: LIBREDCM, tls: {ca_file: /pki/ca.crt}}
EOF
cp $E/scripts/plans/*.yaml "$OUT/"
chmod 644 "$OUT"/*.yaml
# Store plans live next to the image's fixtures: a fresh named volume is
# filled from the image's /opt/libredvt/plans, then the plans are added.
docker volume rm dvt03-plans >/dev/null 2>&1
docker run --rm -u 0 -v dvt03-plans:/opt/libredvt/plans -v "$PWD/$E/scripts/plans":/p:ro --entrypoint cp "$DVT_IMG" \
  /p/store-dcmtk-tls.yaml /p/store-libredcm-tls.yaml /opt/libredvt/plans/ || echo "store plans not copied"

run() { # label target plan (a bare name is under /out; an absolute path is used as is)
  local plan="/out/$3"
  case "$3" in /*) plan="$3" ;; esac
  docker run --rm --name dvt-runner --network $NET -v "$PKI":/pki:ro -v "$PWD/$OUT":/out -v dvt03-plans:/plans:ro "$DVT_IMG" run -targets /out/targets.yaml -target "$2" \
    -junit "/out/$1-junit.xml" -html "/out/$1-report.html" -json "/out/$1-run.json" "$plan" > "$OUT/$1.log" 2>&1
  local rc=$?
  echo "$1 (target $2, plan $3): exit $rc :: $(tail -1 "$OUT/$1.log")"
}
echo "== LibreDVT against DCMTK and LibreDCM"
run plain-negotiation dcmtk-plain associate-dcmtk.yaml
run refuse dcmtk-refuse associate-refused.yaml
run tls-negotiation dcmtk-tls associate-dcmtk-tls.yaml
run tls-refusals dcmtk-tls tls-refusals.yaml
run tls-no-client-cert-not-required dcmtk-tls tls-no-client-cert.yaml
run mtls dcmtk-mtls tls-mtls.yaml
run mtls-stranger dcmtk-mtls-stranger echo.yaml
run wrong-host dcmtk-wrong echo.yaml
run aes-profile-old-versions dcmtk-aes tls-old-versions.yaml
run libredcm-tls libredcm libredcm-tls.yaml
run libredcm-no-client-cert libredcm tls-no-client-cert.yaml
run store-dcmtk-tls dcmtk-tls /plans/store-dcmtk-tls.yaml
run store-libredcm-tls libredcm /plans/store-libredcm-tls.yaml
echo "files storescp +tls wrote: $(docker exec dcmtk-tls sh -c 'ls /tmp | grep -c ^CT')"

echo "== DCMTK echoscu +tls against the same servers (DCMTK does not check host names)"
dcm() { # label args...
  local l=$1; shift
  docker run --rm --network $NET -v "$PKI":/pki:ro --entrypoint echoscu "$DCMTK_IMG" "$@" > "$OUT/dcmtk-$l.log" 2>&1
  local rc=$?
  echo "echoscu $l: exit $rc"
}
dcm tls +tls /pki/libredvt-client.key /pki/libredvt-client.crt +cf /pki/ca.crt dcmtk-tls 11112
dcm mtls +tls /pki/libredvt-client.key /pki/libredvt-client.crt +cf /pki/ca.crt dcmtk-mtls 11112
dcm mtls-no-cert +tla +cf /pki/ca.crt -aet NOCERT dcmtk-mtls 11112
dcm libredcm +tls /pki/libredvt-client.key /pki/libredvt-client.crt +cf /pki/ca.crt -aec LIBREDCM libredcm 11112
dcm libredcm-plain -aec LIBREDCM libredcm 11112
dcm aes-profile +tls /pki/libredvt-client.key /pki/libredvt-client.crt +cf /pki/ca.crt +pa dcmtk-aes 11112

echo "== server logs"
for c in dcmtk-plain dcmtk-refuse dcmtk-tls dcmtk-mtls dcmtk-wrong dcmtk-aes libredcm; do docker logs "$c" > "$OUT/server-$c.log" 2>&1; done
grep -h -c "Association Received" "$OUT"/server-dcmtk-*.log | paste -sd' ' | sed 's/^/DCMTK "Association Received" lines per server (aes mtls plain refuse tls wrong): /'
echo "== TLS versions and suites each DCMTK server recorded (its own log, independent of LibreDVT's worker)"
for c in dcmtk-tls dcmtk-mtls dcmtk-wrong dcmtk-aes; do
  echo "$c: $(grep -E 'Protocol *:|Ciphersuite *:|TLS error' "$OUT/server-$c.log" | sed -E 's/^[DE]: *//; s/ +/ /g' | sort | uniq -c | sed 's/^ *//' | paste -sd';' -)"
done
python3 $E/scripts/summarize.py "$OUT" "$PKI"
echo "summary exit $?"
