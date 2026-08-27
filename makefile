CLI_DIR := packages/cli
TS_DIR  := packages/ts
PY_DIR  := packages/py

VERSION ?= dev
LDFLAGS := -X 'main.Version=$(VERSION)'

.PHONY: help build build-cli build-ts build-py dev-bin install-bin clean test-py

help:
	@echo "build      - build every package"
	@echo "build-cli  - cross-compile the Go CLI for linux/amd64, linux/arm64, darwin/arm64"
	@echo "build-ts   - tsc the TypeScript SDK"
	@echo "build-py   - poetry build the Python SDK"
	@echo "dev-bin    - build the CLI for this machine and drop it into the SDK packages"
	@echo "install-bin- drop an already built CLI into the SDK packages"
	@echo "test-py    - run the Python test suite"
	@echo "clean      - remove build output and dev binaries"

build: build-cli build-ts build-py

build-cli:
	cd $(CLI_DIR) && VERSION=$(VERSION) ./build.sh

build-ts:
	cd $(TS_DIR) && yarn install && yarn build

build-py:
	cd $(PY_DIR) && poetry build

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
	chmod +x $(TS_DIR)/ryskV12 $(PY_DIR)/ryskV12cli
	@echo "CLI $(VERSION) installed at $(TS_DIR)/ryskV12 and $(PY_DIR)/ryskV12cli"

test-py:
	cd $(PY_DIR) && poetry run pytest

clean:
	rm -f $(CLI_DIR)/ryskV12-* $(TS_DIR)/ryskV12 $(PY_DIR)/ryskV12cli
	rm -rf $(TS_DIR)/dist $(PY_DIR)/dist
