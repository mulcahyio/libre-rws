# Tags are vMAJOR.MINOR.PATCH[-beta.N]; the leading "v" is dropped for binaries and images.
VERSION ?= $(patsubst v%,%,$(shell git describe --tags --always 2>/dev/null || echo dev))
export CGO_ENABLED=0

.PHONY: build test test-postgres vet image up cluster down conformance workstation
build:        ## static binary in ./bin
	go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o bin/rws-dicom ./modules/dicom/cmd/rws-dicom
test:         ## unit + end-to-end DIMSE tests
	go test ./modules/... ./platform/... -count=1
test-postgres: ## same tests, index suites also against a throwaway PostgreSQL (needs Docker)
	docker rm -f rws-test-pg >/dev/null 2>&1 || true
	docker run -d --rm --name rws-test-pg -e POSTGRES_PASSWORD=t -e POSTGRES_DB=rws -p 127.0.0.1:55433:5432 postgres:16-alpine >/dev/null
	for i in $$(seq 1 30); do docker exec rws-test-pg pg_isready -U postgres >/dev/null 2>&1 && break; sleep 1; done
	RWS_TEST_POSTGRES_DSN="postgres://postgres:t@127.0.0.1:55433/rws?sslmode=disable" go test -p 1 ./modules/... ./platform/... -count=1; s=$$?; docker rm -f rws-test-pg >/dev/null; exit $$s
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
