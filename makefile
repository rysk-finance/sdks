CLI_DIR := packages/cli
TS_DIR  := packages/ts
PY_DIR  := packages/py
RS_DIR  := packages/rs
CPP_DIR := packages/cpp

VERSION ?= dev
LDFLAGS := -X 'main.Version=$(VERSION)'

.PHONY: help build build-cli build-ts build-py build-rs build-cpp dev-bin install-bin clean test-py test-rs test-cpp

help:
	@echo "build      - build every package"
	@echo "build-cli  - cross-compile the Go CLI for linux/amd64, linux/arm64, darwin/arm64"
	@echo "build-ts   - tsc the TypeScript SDK"
	@echo "build-py   - poetry build the Python SDK"
	@echo "build-rs   - cargo build the Rust SDK"
	@echo "build-cpp  - cmake build the C++ SDK"
	@echo "dev-bin    - build the CLI for this machine and drop it into the SDK packages"
	@echo "install-bin- drop an already built CLI into the SDK packages"
	@echo "test-py    - run the Python test suite"
	@echo "test-rs    - run the Rust test suite"
	@echo "test-cpp   - run the C++ test suite"
	@echo "clean      - remove build output and dev binaries"

build: build-cli build-ts build-py build-rs build-cpp

build-cli:
	cd $(CLI_DIR) && VERSION=$(VERSION) ./build.sh

build-ts:
	cd $(TS_DIR) && yarn install && yarn build

build-py:
	cd $(PY_DIR) && poetry build

build-rs:
	cd $(RS_DIR) && cargo build --release

build-cpp:
	cmake -S $(CPP_DIR) -B $(CPP_DIR)/build -DCMAKE_BUILD_TYPE=Release
	cmake --build $(CPP_DIR)/build

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
	cp $(CLI_DIR)/ryskV12-dev $(CPP_DIR)/ryskV12
	chmod +x $(TS_DIR)/ryskV12 $(PY_DIR)/ryskV12cli $(RS_DIR)/ryskV12 $(CPP_DIR)/ryskV12
	@echo "CLI $(VERSION) installed into ts, py, rs and cpp"

test-py:
	cd $(PY_DIR) && poetry run pytest

test-rs:
	cd $(RS_DIR) && cargo test

# ctest runs each suite from $(CPP_DIR), which is where dev-bin leaves the binary
test-cpp: build-cpp
	ctest --test-dir $(CPP_DIR)/build --output-on-failure

clean:
	rm -f $(CLI_DIR)/ryskV12-* $(TS_DIR)/ryskV12 $(PY_DIR)/ryskV12cli $(RS_DIR)/ryskV12 $(CPP_DIR)/ryskV12
	rm -rf $(TS_DIR)/dist $(PY_DIR)/dist $(RS_DIR)/target $(CPP_DIR)/build
