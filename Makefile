# Based on Alex Edwards' "A time-saving Makefile for your Go projects"
# (https://www.alexedwards.net/blog/a-time-saving-makefile-for-your-go-projects, MIT).

main_package_path = ./cmd/woolies
binary_name = woolies
binary = bin/${binary_name}
# Where `make install` puts the symlink: $(PREFIX)/bin/woolies.
PREFIX ?= $(shell brew --prefix 2>/dev/null || echo /usr/local)

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
	@printf 'Are you sure? [y/N] ' && read ans && [ $${ans:-N} = y ]

.PHONY: no-dirty
no-dirty:
	@test -z "$(shell git status --porcelain)"

# ==================================================================================== #
# QUALITY CONTROL
# ==================================================================================== #

## audit: run quality control checks (tidy, verify, gofmt, vet, staticcheck, govulncheck, tests)
.PHONY: audit
audit: test
	go mod tidy -diff
	go mod verify
	test -z "$(shell gofmt -l .)"
	go vet ./...
	go run honnef.co/go/tools/cmd/staticcheck@latest -checks=all,-ST1000,-U1000 ./...
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

## test: run all tests (offline; never opens the browser)
.PHONY: test
test:
	go test -race -buildvcs ./...

## test/cover: run all tests and display coverage
.PHONY: test/cover
test/cover:
	go test -race -buildvcs -coverprofile=/tmp/woolies-coverage.out ./...
	go tool cover -html=/tmp/woolies-coverage.out

## upgradeable: list direct dependencies with upgrades, read-only (keep chromedp at v0.14.2)
.PHONY: upgradeable
upgradeable:
	@go list -m -u -f '{{if and (not .Indirect) (not .Main) .Update}}{{.Path}} {{.Version}} -> {{.Update.Version}}{{end}}' all

# ==================================================================================== #
# DEVELOPMENT
# ==================================================================================== #

## tidy: tidy modfiles and modernize and format .go files
.PHONY: tidy
tidy:
	go mod tidy -v
	go fix ./...
	go fmt ./...

## build: build bin/woolies
.PHONY: build
build:
	go build -o=${binary} ${main_package_path}

## install: build, then symlink $(PREFIX)/bin/woolies to bin/woolies
.PHONY: install
install: build
	ln -sf "$(CURDIR)/${binary}" "$(PREFIX)/bin/${binary_name}"

## clean: remove the built binary
.PHONY: clean
clean:
	rm -f ${binary}

# ==================================================================================== #
# OPERATIONS
# ==================================================================================== #

## push: audit, check the working tree is clean, then push to the remote
.PHONY: push
push: confirm audit no-dirty
	git push
