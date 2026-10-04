VERSION ?= $(shell git describe --tags --always 2>/dev/null || echo dev)
export CGO_ENABLED=0

.PHONY: build test vet image up cluster down conformance workstation
build:        ## static binary in ./bin
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/rws-dicom ./modules/dicom/cmd/rws-dicom
test:         ## unit + end-to-end DIMSE tests
	go test ./modules/... ./platform/... -count=1
vet:
	go vet ./modules/... ./platform/...
image:        ## container image
	docker build -f deploy/docker/Dockerfile.dicom --build-arg VERSION=$(VERSION) -t rws/dicom:$(VERSION) .
up:           ## single node
	docker compose -f deploy/compose/docker-compose.yml up -d --build
cluster:      ## HA topology on one host (3 gateways)
	docker compose -f deploy/compose/docker-compose.cluster.yml up -d --build --scale dicom=3
down:
	docker compose -f deploy/compose/docker-compose.yml down; docker compose -f deploy/compose/docker-compose.cluster.yml down
conformance:  ## DCMTK smoke test against a running node: make conformance DIR=path/to/dcm
	scripts/conformance.sh $(DIR)
workstation:  ## local workstation install with verification
	deploy/workstation/rws-dicom.sh install
