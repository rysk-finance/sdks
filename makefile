CLI_DIR := packages/cli
TS_DIR  := packages/ts
PY_DIR  := packages/py
RS_DIR  := packages/rs

VERSION ?= dev
LDFLAGS := -X 'main.Version=$(VERSION)'

.PHONY: help build build-cli build-ts build-py build-rs dev-bin install-bin clean test-py test-rs

help:
	@echo "build      - build every package"
	@echo "build-cli  - cross-compile the Go CLI for linux/amd64, linux/arm64, darwin/arm64"
	@echo "build-ts   - tsc the TypeScript SDK"
	@echo "build-py   - poetry build the Python SDK"
	@echo "build-rs   - cargo build the Rust SDK"
	@echo "dev-bin    - build the CLI for this machine and drop it into the SDK packages"
	@echo "install-bin- drop an already built CLI into the SDK packages"
	@echo "test-py    - run the Python test suite"
	@echo "test-rs    - run the Rust test suite"
	@echo "clean      - remove build output and dev binaries"

build: build-cli build-ts build-py build-rs

build-cli:
	cd $(CLI_DIR) && VERSION=$(VERSION) ./build.sh

build-ts:
	cd $(TS_DIR) && yarn install && yarn build

build-py:
	cd $(PY_DIR) && poetry build

build-rs:
	cd $(RS_DIR) && cargo build --release

# Local dev/e2e: the SDKs shell out to a CLI binary next to the package
# (`./ryskV12` for TS, `./ryskV12cli` for python), which in published packages
# is downloaded by
# scripts/fetch_latest_release.sh. Build it from source instead.
dev-bin:
	cd $(CLI_DIR) && go build -ldflags="$(LDFLAGS)" -o ryskV12-dev
	$(MAKE) install-bin

# Drop an already built $(CLI_DIR)/ryskV12-dev into the sdk packages. Split out
# of dev-bin so ci can place a binary it downloaded rather than compiling
# go-ethereum a second time on a second runner.
install-bin:
	cp $(CLI_DIR)/ryskV12-dev $(TS_DIR)/ryskV12
	cp $(CLI_DIR)/ryskV12-dev $(PY_DIR)/ryskV12cli
	cp $(CLI_DIR)/ryskV12-dev $(RS_DIR)/ryskV12
	chmod +x $(TS_DIR)/ryskV12 $(PY_DIR)/ryskV12cli $(RS_DIR)/ryskV12
	@echo "CLI $(VERSION) installed into ts, py and rs"

test-py:
	cd $(PY_DIR) && poetry run pytest

test-rs:
	cd $(RS_DIR) && cargo test

clean:
	rm -f $(CLI_DIR)/ryskV12-* $(TS_DIR)/ryskV12 $(PY_DIR)/ryskV12cli $(RS_DIR)/ryskV12
	rm -rf $(TS_DIR)/dist $(PY_DIR)/dist $(RS_DIR)/target
