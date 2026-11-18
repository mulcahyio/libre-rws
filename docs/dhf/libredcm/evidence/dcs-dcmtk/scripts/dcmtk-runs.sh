#!/usr/bin/env bash
# DCMTK runs for the LibreDCM DICOM Conformance Statement. Runs inside the
# rws-e2e-dcmtk container (hostname dcs-tools) on the evidence network.
# Logs go to /out; test data is in /data (gen-data.py).
set -u
D=/data
GW=dcs-gw
n=10
run() { # run NAME CMD...  -> /out/NN-NAME.log, prints exit status
  local name=$1; shift
  local f
  f=$(printf "/out/%02d-%s.log" "$n" "$name")
  n=$((n + 1))
  { echo "\$ $*"; echo; "$@"; local rc=$?; echo; echo "exit status: $rc"; } > "$f" 2>&1
  echo "$(basename "$f"): $(tail -1 "$f")"
}
S1=1.3.6.1.4.1.67686.9.1.900.1
S2=1.3.6.1.4.1.67686.9.1.900.2

echo "== DCMTK: $(echoscu --version | head -1)"

# --- Verification and association acceptance
run echoscu                 echoscu -d -aet DCMTKECHO -aec LIBREDCM $GW 11112
run echoscu-wrong-called-ae echoscu -d -aet DCMTKECHO -aec NOTLIBREDCM $GW 11112
run echoscu-allowlist-ok    echoscu -d -aet DCMTKOK -aec LIBREDCM dcs-allow 11112
run echoscu-allowlist-refused echoscu -d -aet INTRUDER -aec LIBREDCM dcs-allow 11112

# --- Storage SCP
run storescu-native        storescu -d -aet DCMTKSTORE -aec LIBREDCM $GW 11112 $D/s1-ct-explicit.dcm $D/s1-mr-implicit.dcm
# dcmsend proposes each file's own (compressed) transfer syntax first, then the
# uncompressed ones; storescu would propose uncompressed syntaxes only.
run dcmsend-compressed     dcmsend -d -aet DCMTKSEND -aec LIBREDCM $GW 11112 \
  $D/s2-mr-rle.dcm $D/s2-mr-j2k-lossless.dcm $D/s2-mr-jpegls-lossless.dcm \
  $D/s2-sc-jpeg-extended.dcm $D/s2-sc-jpeg-baseline.dcm $D/s2-sc-jpeg-lossless-sv1.dcm $D/s2-sc-j2k.dcm
# -xb: storescu proposes a context with Explicit VR Big Endian only and one
# with the little endian syntaxes for each class; LibreDCM must refuse the first.
run storescu-bigendian     storescu -d -xb -aet DCMTKSTORE -aec LIBREDCM $GW 11112 $D/s3-mr-bigendian.dcm
run storescu-no-study-uid  storescu -d -aet DCMTKSTORE -aec LIBREDCM $GW 11112 $D/neg-no-study-uid.dcm
run storescu-duplicate     storescu -v -aet DCMTKSTORE -aec LIBREDCM $GW 11112 $D/s1-ct-explicit.dcm
run storescu-conflict-quarantine storescu -v -aet DCMTKSTORE -aec LIBREDCM $GW 11112 $D/conflict-s1-other-patient.dcm
run storescu-conflict-reject storescu -d -aet DCMTKSTORE -aec LIBREDCM dcs-reject 11112 $D/s1-ct-explicit.dcm $D/s1-mr-implicit.dcm $D/conflict-s1-other-patient.dcm
run storescu-conflict-coerce storescu -d -aet DCMTKSTORE -aec LIBREDCM dcs-coerce 11112 $D/s1-ct-explicit.dcm $D/conflict-s1-other-patient.dcm

# --- Query SCP (C-FIND)
run findscu-patient-root-patient findscu -v -P -aet DCMTKFIND -aec LIBREDCM $GW 11112 \
  -k QueryRetrieveLevel=PATIENT -k PatientID= -k PatientName= -k PatientBirthDate= -k PatientSex= -k NumberOfPatientRelatedStudies=
run findscu-study-root-study findscu -v -S -aet DCMTKFIND -aec LIBREDCM $GW 11112 \
  -k QueryRetrieveLevel=STUDY -k "PatientName=dcs*" -k PatientID= -k StudyInstanceUID= -k StudyDate= -k StudyTime= \
  -k AccessionNumber= -k ModalitiesInStudy= -k NumberOfStudyRelatedSeries= -k NumberOfStudyRelatedInstances= -k InstitutionName=
run findscu-study-date-range findscu -v -S -aet DCMTKFIND -aec LIBREDCM $GW 11112 \
  -k QueryRetrieveLevel=STUDY -k StudyDate=20260201- -k StudyInstanceUID= -k PatientID=
run findscu-modalities-in-study findscu -v -S -aet DCMTKFIND -aec LIBREDCM $GW 11112 \
  -k QueryRetrieveLevel=STUDY -k 'ModalitiesInStudy=CT\OT' -k StudyInstanceUID=
run findscu-utf8-name findscu -v -S -aet DCMTKFIND -aec LIBREDCM $GW 11112 \
  -k SpecificCharacterSet="ISO_IR 192" -k QueryRetrieveLevel=STUDY -k "PatientName=müller*" -k StudyInstanceUID=
run findscu-series-uid-list findscu -v -S -aet DCMTKFIND -aec LIBREDCM $GW 11112 \
  -k QueryRetrieveLevel=SERIES -k "StudyInstanceUID=$S1\\$S2" -k SeriesInstanceUID= -k Modality= -k SeriesNumber= -k NumberOfSeriesRelatedInstances= -k StudyDate=
run findscu-image findscu -v -S -aet DCMTKFIND -aec LIBREDCM $GW 11112 \
  -k QueryRetrieveLevel=IMAGE -k StudyInstanceUID=$S1 -k SeriesInstanceUID= -k SOPInstanceUID= -k SOPClassUID= -k InstanceNumber= -k PatientName=
run findscu-patient-level-in-study-root findscu -v -S -aet DCMTKFIND -aec LIBREDCM $GW 11112 \
  -k QueryRetrieveLevel=PATIENT -k PatientID=
run findscu-conflict-hidden findscu -v -S -aet DCMTKFIND -aec LIBREDCM $GW 11112 \
  -k QueryRetrieveLevel=IMAGE -k StudyInstanceUID=$S1 -k SOPInstanceUID= -k PatientID=
run findscu-find-limit findscu -v -S -aet DCMTKFIND -aec LIBREDCM dcs-reject 11112 \
  -k QueryRetrieveLevel=IMAGE -k StudyInstanceUID=$S1 -k SOPInstanceUID=

# --- Move SCP (C-MOVE); DCMTKMOVE is configured as dcs-tools:11113
mkdir -p /tmp/rx1 /tmp/rx2 /tmp/rx3
run movescu-native movescu -d -S -aet DCMTKMOVE -aem DCMTKMOVE -aec LIBREDCM --port 11113 -od /tmp/rx1 \
  -k QueryRetrieveLevel=STUDY -k StudyInstanceUID=$S1 $GW 11112
run movescu-compressed-dest-uncompressed-only movescu -v -S -aet DCMTKMOVE -aem DCMTKMOVE -aec LIBREDCM --port 11113 -od /tmp/rx2 \
  -k QueryRetrieveLevel=STUDY -k StudyInstanceUID=$S2 $GW 11112
run movescu-compressed-dest-accepts-all movescu -v -S +xa -aet DCMTKMOVE -aem DCMTKMOVE -aec LIBREDCM --port 11113 -od /tmp/rx3 \
  -k QueryRetrieveLevel=STUDY -k StudyInstanceUID=$S2 $GW 11112
run movescu-unknown-destination movescu -v -S -aet DCMTKMOVE -aem NOWHERE -aec LIBREDCM \
  -k QueryRetrieveLevel=STUDY -k StudyInstanceUID=$S1 $GW 11112
run movescu-universal-refused movescu -v -S -aet DCMTKMOVE -aem DCMTKMOVE -aec LIBREDCM --port 11113 -od /tmp/rx1 \
  -k QueryRetrieveLevel=STUDY -k StudyInstanceUID= $GW 11112
run movescu-patient-root movescu -v -P -aet DCMTKMOVE -aem DCMTKMOVE -aec LIBREDCM --port 11113 -od /tmp/rx1 \
  -k QueryRetrieveLevel=PATIENT -k PatientID=DCS-P1 $GW 11112

# --- Get SCP (C-GET)
mkdir -p /tmp/get1 /tmp/get2
run getscu-native getscu -d -S -aet DCMTKGET -aec LIBREDCM -od /tmp/get1 \
  -k QueryRetrieveLevel=STUDY -k StudyInstanceUID=$S1 $GW 11112
run getscu-compressed getscu -v -S -aet DCMTKGET -aec LIBREDCM -od /tmp/get2 \
  -k QueryRetrieveLevel=STUDY -k StudyInstanceUID=$S2 $GW 11112

# --- DICOM TLS (dcs-tls has a certificate made by LibreDCM -gen-cert)
run echoscu-tls echoscu -d +tla +cf /certs/tls.crt -aet DCMTKTLS -aec LIBREDCM dcs-tls 11112
run echoscu-plain-to-tls echoscu -v -to 10 -ta 10 -aet DCMTKTLS -aec LIBREDCM dcs-tls 11112

echo "== files received by movescu: rx1=$(ls /tmp/rx1 | wc -l) rx2=$(ls /tmp/rx2 | wc -l) rx3=$(ls /tmp/rx3 | wc -l); getscu: get1=$(ls /tmp/get1 | wc -l) get2=$(ls /tmp/get2 | wc -l)"
for f in /tmp/rx3/*; do echo "rx3 $(basename "$f"): $(dcmdump -M +P 0002,0010 "$f" 2>&1 | head -1)"; done
