# Makefile — clawde-intelligence build targets.
#
# Purpose: Build, test, and proto-generation targets.
# Usage:   make proto | make build | make test | make lint
#
# proto target requires: protoc, protoc-gen-go, protoc-gen-go-grpc,
#   protoc-gen-grpc-gateway, and the googleapis protos on GOPATH.
# Install with:
#   go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
#   go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
#   go install github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway@latest
#
# NOTE: protoc-gen-go/grpc/grpc-gateway are NOT available in this build
# environment. The generated stubs are hand-written in
# internal/server/gateway_grpc_gen.go. Run `make proto` once the tools
# are installed to regenerate and replace the hand-written stubs.

PROTO_SRC  := proto/gateway.proto
PROTO_OUT  := internal/server
# googleapis: use the bundled path from grpc-gateway v1 (contains google/api/*.proto).
# grpc-gateway v2 dropped third_party/googleapis; use v1 for these well-known protos.
GOOGLEAPIS := $(shell go env GOPATH)/pkg/mod/github.com/grpc-ecosystem/grpc-gateway@v1.16.0/third_party/googleapis

.PHONY: proto build test lint clean eval-smoke eval eval-gate eval-bench

## proto: regenerate gRPC + grpc-gateway stubs from proto/gateway.proto
proto:
	@echo "Generating protobuf stubs..."
	PATH="$$(go env GOPATH)/bin:$$PATH" protoc \
		-I proto \
		-I $(GOOGLEAPIS) \
		--go_out=$(PROTO_OUT) --go_opt=paths=source_relative \
		--go-grpc_out=$(PROTO_OUT) --go-grpc_opt=paths=source_relative \
		--grpc-gateway_out=$(PROTO_OUT) --grpc-gateway_opt=paths=source_relative \
		$(PROTO_SRC)
	@echo "Done. Generated stubs in $(PROTO_OUT)."

## build: compile all packages
build:
	go build ./...

## test: run all tests with race detector
test:
	go test ./... -count=1 -race

## lint: run go vet
lint:
	go vet ./...

## eval-smoke: run 10-query subset of the A/B eval harness (CI-safe, no live DB/TEI)
eval-smoke:
	@echo "Running eval smoke (10 queries, mock driver + searcher)..."
	go run ./cmd/eval --provider bge-m3 --dataset eval/testdata/golden_queries.jsonl --smoke
	@echo "eval-smoke PASSED"

## eval: run the cascade benchmark eval (smoke driver + searcher) over the
##       200-item cascade golden set, then capture the benchmark baseline.
eval:
	@echo "Running cascade eval (200 items, 50/tier, mock driver + searcher)..."
	go run ./cmd/eval --provider bge-m3 --dataset eval/testdata/golden_cascade.jsonl --smoke
	@echo "Capturing benchmark baseline..."
	$(MAKE) eval-bench
	@echo "eval PASSED"

## eval-bench: run Go benchmarks for retrieval latency + embedding throughput.
eval-bench:
	go test ./internal/eval -run='^$$' -bench=. -benchmem

## eval-gate: run the regression gate + BGE-vs-Gemini tie-break (CI-safe,
##           baseline-null aware). Exits non-zero on regression.
eval-gate:
	@echo "Running eval gate (regression + tie-break)..."
	go run ./cmd/eval-gate \
		--baseline eval/testdata/baseline.json \
		--bge eval/testdata/result_bge.json \
		--gemini eval/testdata/result_gemini.json
	@echo "eval-gate PASSED"

## clean: remove generated protobuf artifacts
clean:
	find $(PROTO_OUT) -name "*.pb.go" -delete
	find $(PROTO_OUT) -name "*_grpc.pb.go" -delete
	find $(PROTO_OUT) -name "*.pb.gw.go" -delete
