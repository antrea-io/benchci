SHELL              := /bin/bash
# go options
GO                 ?= go
LDFLAGS            :=
GOFLAGS            :=
BINDIR             ?= $(CURDIR)/bin

all: bin

.PHONY: bin
bin:
	@mkdir -p $(BINDIR)
	GOOS=linux $(GO) build -o $(BINDIR) $(GOFLAGS) -ldflags '$(LDFLAGS)' github.com/antoninbas/benchci/...

.PHONY: test
test:
	@echo "==> Running all tests <=="
	GOOS=linux $(GO) test ./test

# code linting
.golangci-bin:
	@echo "===> Installing Golangci-lint <==="
	@curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $@ v1.41.1

.PHONY: golangci
golangci: .golangci-bin
	@echo "===> Running golangci <==="
	@GOOS=linux .golangci-bin/golangci-lint run -c .golangci.yml

.PHONY: golangci-fix
golangci-fix: .golangci-bin
	@echo "===> Running golangci-fix <==="
	@GOOS=linux .golangci-bin/golangci-lint run -c .golangci.yml --fix
