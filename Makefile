# Change these variables as necessary.
main_package_path = ./cmd/novamix
binary_name = novamix

# The CLIs live in their own module so the app doesn't inherit their
# dependency trees. They are built natively once (the code they analyse is
# Windows-only, so they run with GOOS=windows set, which "go tool" would
# otherwise use to build the tool itself).
tools = -modfile=tools/go.mod
tools_bin = bin/tools

app_version = $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
version_flags = -X main.version=$(app_version)

# ==================================================================================== #
# HELPERS
# ==================================================================================== #

## help: print this help message
.PHONY: help
help:
	@echo 'Usage:'
	@sed -n 's/^##//p' ${MAKEFILE_LIST} | column -t -s ':' | sed -e 's/^/ /'

.PHONY: confirm
confirm:
	@echo -n 'Are you sure? [y/N] ' && read ans && [ $${ans:-N} = y ]

.PHONY: no-dirty
no-dirty:
	@test -z "$(shell git status --porcelain)"

$(tools_bin)/modernize: tools/go.mod tools/go.sum
	go build $(tools) -o $@ golang.org/x/tools/go/analysis/passes/modernize/cmd/modernize

$(tools_bin)/govulncheck: tools/go.mod tools/go.sum
	go build $(tools) -o $@ golang.org/x/vuln/cmd/govulncheck

## tools: build the pinned analysis tools into bin/tools/
.PHONY: tools
tools: $(tools_bin)/modernize $(tools_bin)/govulncheck

# ==================================================================================== #
# QUALITY CONTROL
# ==================================================================================== #

## audit: run quality control checks
.PHONY: audit
audit: test tools
	go mod tidy -diff
	go mod verify
	test -z "$(shell gofmt -l .)"
	test -z "$(shell GOOS=windows $(tools_bin)/modernize -test -fix -diff ./...)"
	GOOS=windows go vet ./...
	GOOS=windows $(tools_bin)/govulncheck ./...
	GOOS=windows go build -o /dev/null $(main_package_path)
	cd tools && go mod tidy -diff
	cd tools && go mod verify

## test: run the platform-independent tests (protocol parsing, app classification)
.PHONY: test
test:
	go test -race -buildvcs ./...

## test/cover: run all tests and display coverage
.PHONY: test/cover
test/cover:
	go test -race -buildvcs -coverprofile=/tmp/coverage.out ./...
	go tool cover -html=/tmp/coverage.out

# ==================================================================================== #
# DEVELOPMENT
# ==================================================================================== #

## tidy: tidy modfiles and format .go files
.PHONY: tidy
tidy: tools
	go mod tidy -v
	cd tools && go mod tidy -v
	GOOS=windows $(tools_bin)/modernize -test -fix ./...
	go fmt ./...

## build: build the Windows binary into bin/ (no console window)
.PHONY: build
build:
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -H=windowsgui $(version_flags)" -o bin/$(binary_name).exe $(main_package_path)

## build/debug: build with a console window attached (log output visible)
.PHONY: build/debug
build/debug:
	GOOS=windows GOARCH=amd64 go build -ldflags="$(version_flags)" -o bin/$(binary_name)-debug.exe $(main_package_path)

# Launching a fresh exe straight off \\wsl$ hangs before main() (Defender
# scans the unsigned binary over the network path, sometimes forever), so
# run copies it to the Windows temp dir first.
win_temp = $(shell wslpath "$$(/mnt/c/Windows/System32/cmd.exe /c 'echo %TEMP%' 2>/dev/null | tr -d '\r')")

## run: build the console binary and launch it from WSL2 via Windows interop
.PHONY: run
run: build/debug
	cp bin/$(binary_name)-debug.exe "$(win_temp)/$(binary_name)-debug.exe"
	"$(win_temp)/$(binary_name)-debug.exe" -v

## run/dump: launch with raw HID report dumping (protocol debugging)
.PHONY: run/dump
run/dump: build/debug
	cp bin/$(binary_name)-debug.exe "$(win_temp)/$(binary_name)-debug.exe"
	"$(win_temp)/$(binary_name)-debug.exe" -v -dump

# ==================================================================================== #
# OPERATIONS
# ==================================================================================== #

## install: build and register the logon task on the Windows host (from WSL2), e.g. make install args='-fallback Speakers'
.PHONY: install
install: build
	cp bin/$(binary_name).exe "$(win_temp)/$(binary_name).exe"
	"$(win_temp)/$(binary_name).exe" -install $(args)

## uninstall: stop and remove the logon task on the Windows host
.PHONY: uninstall
uninstall: build
	cp bin/$(binary_name).exe "$(win_temp)/$(binary_name).exe"
	"$(win_temp)/$(binary_name).exe" -uninstall

.PHONY: release/version
release/version:
	@echo '$(version)' | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$$' || { echo 'usage: make release version=vX.Y.Z'; exit 1; }

## release: tag and push a release, e.g. make release version=v0.1.0
.PHONY: release
release: release/version no-dirty audit confirm
	git tag -a $(version) -m '$(version)'
	git push origin $(version)
