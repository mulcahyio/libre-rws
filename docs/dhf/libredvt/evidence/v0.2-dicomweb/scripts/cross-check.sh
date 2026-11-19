#!/bin/bash
# LibreDVT v0.2 card DVT-0.2/4: DICOMweb cross-check against an independent
# implementation, Orthanc with its DICOMweb plugin (C++, civetweb, OpenSSL;
# none of LibreDVT's code), in a digest-pinned test container:
#   - HTTPS audit plan: refusal probes, STOW-RS with verify_retrieve,
#     QIDO-RS at three levels with paging and no-match, WADO-RS with pixel
#     comparison, an explicit transfer syntax and metadata.
#   - Orthanc's own REST API then serves every stored file: each must be
#     byte-identical to a fixture (end-to-end check outside DICOMweb).
#   - Mutual TLS, a server without client authentication (negative), a
#     certificate for another host name (negative).
#   - A bearer token over loopback http; every artefact is grepped for it.
# Certificates come from the openssl command line on the host (RSA, a
# synthetic test CA), so neither Go's nor Python's TLS code made them.
#
#   bash docs/dhf/libredvt/evidence/v0.2-dicomweb/scripts/cross-check.sh   # from the repository root
# Needs: libre/dvt:v02-dicomweb (this branch), network access to pull the
# pinned Orthanc image once.
cd "$(git rev-parse --show-toplevel)" || exit 1
E=docs/dhf/libredvt/evidence/v0.2-dicomweb
OUT=$E/cross-check
DVT_IMG=${DVT_IMG:-libre/dvt:v02-dicomweb}
ORTHANC_IMG=orthancteam/orthanc:26.11.1@sha256:25738bcd515926ac8d33cacf09729a410a32c0a65d6cb8e61bd86fce81a4cd30
NET=dvt04-net
PKI=$(mktemp -d "${TMPDIR:-/tmp}/dvt04-pki.XXXXXX")
TOKEN="dvt-crosscheck-synthetic-$(openssl rand -hex 12)"  # synthetic, new per run
mkdir -p "$OUT"; chmod 777 "$OUT"
echo "commit $(git rev-parse --short HEAD), $(date -u +%Y-%m-%dT%H:%M:%SZ)"
docker pull -q "$ORTHANC_IMG" > /dev/null || { echo "cannot pull $ORTHANC_IMG"; exit 2; }
echo "image $DVT_IMG $(docker inspect -f '{{.Id}}' "$DVT_IMG")"
echo "image $ORTHANC_IMG $(docker inspect -f '{{.Id}}' "$ORTHANC_IMG")"

echo "== synthetic PKI (openssl $(openssl version | cut -d' ' -f2))"
cd "$PKI" || exit 1
openssl req -x509 -newkey rsa:2048 -nodes -keyout ca.key -out ca.crt -days 2 -subj "/O=LibreDVT cross-check/CN=dvt04-test-ca" \
  -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" 2>/dev/null
leaf() { # name CN SAN ext
  openssl req -newkey rsa:2048 -nodes -keyout "$1.key" -out "$1.csr" -subj "/O=LibreDVT cross-check/CN=$2" 2>/dev/null
  printf 'subjectAltName=%s\nextendedKeyUsage=%s\nbasicConstraints=CA:FALSE\n' "$3" "$4" > "$1.ext"
  openssl x509 -req -in "$1.csr" -CA ca.crt -CAkey ca.key -CAcreateserial -days 1 -extfile "$1.ext" -out "$1.crt" 2>/dev/null
}
leaf orthanc orthanc "DNS:orthanc-https,DNS:orthanc-mtls" serverAuth
leaf libredvt-client libredvt-client "DNS:libredvt-client" clientAuth
cat orthanc.crt orthanc.key > orthanc.pem
printf '%s\n' "$TOKEN" > token
rm -f ./*.csr ./*.ext ./*.srl
chmod 755 "$PKI"; chmod 644 "$PKI"/*
echo "orthanc: $(openssl x509 -in orthanc.crt -noout -subject -ext subjectAltName | tr '\n' ' ')"
cd - >/dev/null || exit 1

docker network rm $NET >/dev/null 2>&1; docker network create $NET >/dev/null
cleanup() { docker rm -f orthanc-https orthanc-mtls orthanc-plain >/dev/null 2>&1; docker network rm $NET >/dev/null 2>&1; }
trap cleanup EXIT
orthanc() { # name extra-docker-args... -- extra env as -e pairs
  docker run -d --name "$1" "${@:2}" -v "$PKI":/pki:ro \
    -e DICOM_WEB_PLUGIN_ENABLED=true -e ORTHANC__AUTHENTICATION_ENABLED=false -e ORTHANC__NAME="$1" \
    "$ORTHANC_IMG" >/dev/null
}
TLSENV=(-e ORTHANC__SSL_ENABLED=true -e ORTHANC__SSL_CERTIFICATE=/pki/orthanc.pem -e ORTHANC__SSL_MINIMUM_PROTOCOL_VERSION=4 -e ORTHANC__DICOM_WEB__SSL=true)
orthanc orthanc-https --network $NET --network-alias orthanc-alias "${TLSENV[@]}"
orthanc orthanc-mtls --network $NET "${TLSENV[@]}" -e ORTHANC__SSL_VERIFY_PEERS=true -e ORTHANC__SSL_TRUSTED_CLIENT_CERTIFICATES=/pki/ca.crt
orthanc orthanc-plain -p 127.0.0.1:18042:8042
for c in orthanc-https orthanc-mtls orthanc-plain; do
  for _ in $(seq 1 60); do docker logs "$c" 2>&1 | grep -q "Orthanc has started" && break; sleep 1; done
  echo "$c: $(docker logs "$c" 2>&1 | grep -c 'Orthanc has started') start line(s)"
done

cat > "$OUT/targets.yaml" <<'EOF'
targets:
  - name: orthanc-https
    class: lab
    protocol: dicomweb
    base_url: https://orthanc-https:8042/dicom-web
    tls: {ca_file: /pki/ca.crt}
  - name: orthanc-mtls
    class: lab
    protocol: dicomweb
    base_url: https://orthanc-mtls:8042/dicom-web
    tls: {ca_file: /pki/ca.crt, client_cert_file: /pki/libredvt-client.crt, client_key_file: /pki/libredvt-client.key}
  - name: orthanc-wrong-host
    class: lab
    protocol: dicomweb
    base_url: https://orthanc-alias:8042/dicom-web
    tls: {ca_file: /pki/ca.crt}
  - name: orthanc-loopback
    class: lab
    protocol: dicomweb
    base_url: http://127.0.0.1:18042/dicom-web
    auth: {type: bearer, token_file: /pki/token}
EOF
cp $E/scripts/plans/*.yaml "$OUT/"
chmod 644 "$OUT"/*.yaml

run() { # label target plan [docker network]
  docker run --rm --name dvt-runner --network "${4:-$NET}" -v "$PKI":/pki:ro -v "$PWD/$OUT":/out "$DVT_IMG" run -targets /out/targets.yaml -target "$2" \
    -junit "/out/$1-junit.xml" -html "/out/$1-report.html" -json "/out/$1-run.json" "/out/$3" > "$OUT/$1.log" 2>&1
  local rc=$?
  echo "$1 (target $2, plan $3): exit $rc :: $(tail -1 "$OUT/$1.log")"
}
echo "== LibreDVT against Orthanc"
run orthanc-audit orthanc-https orthanc-audit.yaml
run orthanc-no-client-cert orthanc-https no-client-cert.yaml
run orthanc-mtls orthanc-mtls mtls.yaml
run orthanc-wrong-host orthanc-wrong-host search.yaml
run orthanc-token-loopback orthanc-loopback token-loopback.yaml host

echo "== Orthanc REST API: stored files against the fixtures (outside DICOMweb)"
docker run --rm --network $NET -v "$PKI":/pki:ro -v "$PWD/$E/scripts":/s:ro --entrypoint python3 "$DVT_IMG" \
  /s/orthanc-bytes.py https://orthanc-https:8042 /pki/ca.crt /opt/libredvt/plans/fixtures > "$OUT/orthanc-bytes.log" 2>&1
echo "orthanc-bytes: exit $? :: $(tail -1 "$OUT/orthanc-bytes.log")"; head -1 "$OUT/orthanc-bytes.log"

echo "== Orthanc logs"
for c in orthanc-https orthanc-mtls orthanc-plain; do docker logs "$c" > "$OUT/server-$c.log" 2>&1; done
python3 $E/scripts/summarize.py "$OUT" "$PKI" "$TOKEN"
echo "summary exit $?"
