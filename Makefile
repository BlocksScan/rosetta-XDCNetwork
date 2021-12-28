
# Copyright (c) 2020 XDC.Network

export GO111MODULE=on

# Go parameters
GOCMD=go
GOLINT=golint
GOBUILD=$(GOCMD) build
GOINSTALL=$(GOCMD) install
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
BIN_DIRECTORY=bin
NOFILE=100000

.PHONY: deps build run lint run-mainnet-online run-mainnet-offline run-testnet-online \
	run-testnet-offline check-comments \
	build-local fmt update-tracer \
	update-bootstrap-balances

default: build-local

build:
	docker build -t xdc-rosetta:latest https://github.com/BlocksScan/rosetta-XDCNetwork.git

build-local:
	docker build -t XDC-rosetta:latest .

build-release:
	# make sure to always set version with vX.X.X
	docker build -t XDC-rosetta:$(version) .;
	docker save XDC-rosetta:$(version) | gzip > XDC-rosetta-$(version).tar.gz;

run-mainnet-online:
	docker run -d --rm --ulimit "nofile=${NOFILE}:${NOFILE}" -v "${PWD}/XDC-data:/data" -e "MODE=ONLINE" -e "NETWORK=MAINNET" -e "PORT=8080" -p 8080:8080 -p 30303:30303 XDC-rosetta:latest

run-mainnet-offline:
	docker run -d --rm -e "MODE=OFFLINE" -e "NETWORK=MAINNET" -e "PORT=8081" -p 8081:8081 XDC-rosetta:latest

run-mainnet-remote:
	docker run -d --rm --ulimit "nofile=${NOFILE}:${NOFILE}" -e "MODE=ONLINE" -e "NETWORK=MAINNET" -e "PORT=8080" -e "XDC=$(XDC)" -p 8080:8080 -p 30303:30303 XDC-rosetta:latest

run-testnet-online:
	docker run -d --rm --ulimit "nofile=${NOFILE}:${NOFILE}" -v "${PWD}/XDC-data:/data" -e "MODE=ONLINE" -e "NETWORK=TESTNET" -e "PORT=8080" -p 8080:8080 -p 30303:30303 XDC-rosetta:latest

run-testnet-offline:
	docker run -d --rm -e "MODE=OFFLINE" -e "NETWORK=TESTNET" -e "PORT=8081" -p 8081:8081 XDC-rosetta:latest

run-testnet-remote:
	docker run -d --rm --ulimit "nofile=${NOFILE}:${NOFILE}" -e "MODE=ONLINE" -e "NETWORK=TESTNET" -e "PORT=8080" -e "XDC=$(XDC)" -p 8080:8080 -p 30303:30303 XDC-rosetta:latest

run-devnet-online:
	docker run -d --rm --ulimit "nofile=${NOFILE}:${NOFILE}" -v "${PWD}/XDC-data:/data" -e "MODE=ONLINE" -e "NETWORK=DEVNET" -e "PORT=8080" -p 8080:8080 -p 30303:30303 XDC-rosetta:latest

run-devnet-offline:
	docker run -d --rm -e "MODE=OFFLINE" -e "NETWORK=DEVNET" -e "PORT=8081" -p 8081:8081 XDC-rosetta:latest

run-devnet-remote:
	docker run -d --rm --ulimit "nofile=${NOFILE}:${NOFILE}" -e "MODE=ONLINE" -e "NETWORK=DEVNET" -e "PORT=8080" -e "XDC=$(XDC)" -p 8080:8080 -p 30303:30303 XDC-rosetta:latest


check-comments:
	${GOLINT_CMD} -set_exit_status ${GO_FOLDERS} .

lint: | check-comments
	golangci-lint run --timeout 2m0s -v -E ${LINT_SETTINGS},gomnd


clean:
	@echo "Cleaning..."
	@rm -rf ./$(BIN_DIRECTORY)

deps:
	go get ./...

update-tracer:
	curl https://raw.githubusercontent.com/xinfinorg/XDPoSChain/master/eth/tracers/internal/tracers/call_tracer.js -o XDPoSChain/call_tracer.js
update-bootstrap-balances:
	go run main.go utils:generate-bootstrap XDPoSChain/genesis_files/mainnet.json rosetta-cli-conf/mainnet/bootstrap_balances.json
	go run main.go utils:generate-bootstrap XDPoSChain/genesis_files/testnet.json rosetta-cli-conf/testnet/bootstrap_balances.json
	go run main.go utils:generate-bootstrap XDPoSChain/genesis_files/devnet.json rosetta-cli-conf/devnet/bootstrap_balances.json

gofmt:
	$(GOFMT) -s -w $(GO_FILES)
