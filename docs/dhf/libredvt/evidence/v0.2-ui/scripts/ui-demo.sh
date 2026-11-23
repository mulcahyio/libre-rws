#!/bin/bash
# Card DVT-0.2/5: LibreDVT web UI demo stack for screenshots and a real run of
# the bundled plans. LibreDCM (TLS) and Orthanc + DICOMweb (HTTPS) as targets,
# LibreDVT `serve` on 127.0.0.1:18095. Synthetic PKI from openssl.
#   bash ui-demo-dvt05.sh up | down
S=${DVT05_DIR:-${TMPDIR:-/tmp}/dvt05}
DVT_IMG=${DVT_IMG:-libre/dvt:v02-ui}
DCM_IMG=${DCM_IMG:-libre/dcm:v02-foundation}
ORTHANC_IMG=orthancteam/orthanc:26.11.1@sha256:25738bcd515926ac8d33cacf09729a410a32c0a65d6cb8e61bd86fce81a4cd30
NET=dvt05-net
down() { docker rm -f dvt05-libredvt dvt05-libredcm dvt05-orthanc >/dev/null 2>&1; docker network rm $NET >/dev/null 2>&1; echo down; }
if [ "$1" = down ]; then down; exit 0; fi
down
mkdir -p "$S/pki" "$S/data"
chmod 777 "$S/data"
cd "$S/pki" || exit 1
openssl req -x509 -newkey rsa:2048 -nodes -keyout ca.key -out ca.crt -days 3 -subj "/O=LibreDVT UI demo/CN=dvt05-demo-ca" \
  -addext "basicConstraints=critical,CA:TRUE" -addext "keyUsage=critical,keyCertSign,cRLSign" 2>/dev/null
leaf() {
  openssl req -newkey rsa:2048 -nodes -keyout "$1.key" -out "$1.csr" -subj "/O=LibreDVT UI demo/CN=$2" 2>/dev/null
  printf 'subjectAltName=%s\nextendedKeyUsage=%s\nbasicConstraints=CA:FALSE\n' "$3" "$4" > "$1.ext"
  openssl x509 -req -in "$1.csr" -CA ca.crt -CAkey ca.key -CAcreateserial -days 2 -extfile "$1.ext" -out "$1.crt" 2>/dev/null
}
leaf libredcm libredcm "DNS:libredcm" serverAuth
leaf orthanc orthanc "DNS:orthanc-https" serverAuth
cat orthanc.crt orthanc.key > orthanc.pem
printf 'dvt05-demo-synthetic-%s\n' "$(openssl rand -hex 12)" > token
printf 'dvt05-demo-admin-%s\n' "$(openssl rand -hex 8)" > admin-password
rm -f ./*.csr ./*.ext ./*.srl
chmod 755 . ; chmod 644 ./*
docker network create $NET >/dev/null
docker run -d --name dvt05-libredcm --network $NET --network-alias libredcm -v "$S/pki":/pki:ro \
  -e LIBRE_DCM_TLS_CERT=/pki/libredcm.crt -e LIBRE_DCM_TLS_KEY=/pki/libredcm.key -e LIBRE_DCM_LOG_FORMAT=text "$DCM_IMG" >/dev/null
docker run -d --name dvt05-orthanc --network $NET --network-alias orthanc-https -v "$S/pki":/pki:ro \
  -e DICOM_WEB_PLUGIN_ENABLED=true -e ORTHANC__AUTHENTICATION_ENABLED=false -e ORTHANC__NAME=orthanc-https \
  -e ORTHANC__SSL_ENABLED=true -e ORTHANC__SSL_CERTIFICATE=/pki/orthanc.pem -e ORTHANC__SSL_MINIMUM_PROTOCOL_VERSION=4 -e ORTHANC__DICOM_WEB__SSL=true \
  "$ORTHANC_IMG" >/dev/null
docker run -d --name dvt05-libredvt --network $NET -p 127.0.0.1:18095:8095 -v "$S/pki":/pki:ro -v "$S/data":/var/lib/libredvt \
  -e LIBRE_DVT_ADMIN_PASSWORD_FILE=/pki/admin-password -e LIBRE_DVT_SECRETS_DIR=/pki "$DVT_IMG" serve >/dev/null
for _ in $(seq 1 60); do docker logs dvt05-orthanc 2>&1 | grep -q "Orthanc has started" && break; sleep 1; done
for _ in $(seq 1 40); do [ "$(docker inspect -f '{{.State.Health.Status}}' dvt05-libredcm 2>/dev/null)" = healthy ] && break; sleep 1; done
sleep 2
echo "libredcm: $(docker inspect -f '{{.State.Health.Status}}' dvt05-libredcm)"
echo "orthanc: $(docker logs dvt05-orthanc 2>&1 | grep -c 'Orthanc has started') start line(s)"
echo "libredvt healthz: $(curl -s http://127.0.0.1:18095/healthz)"
for i in "$DVT_IMG" "$DCM_IMG" "$ORTHANC_IMG"; do echo "image $i $(docker inspect -f '{{.Id}}' "$i")"; done
