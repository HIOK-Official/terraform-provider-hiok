BINARY  := terraform-provider-hiok
VERSION ?= 0.1.0
OS_ARCH := $(shell go env GOOS)_$(shell go env GOARCH)
# Where Terraform looks for locally-built providers.
PLUGIN_DIR := $(HOME)/.terraform.d/plugins/registry.terraform.io/HIOK-Official/hiok/$(VERSION)/$(OS_ARCH)

.PHONY: build install fmt vet test testacc demo clean

build:
	go build -ldflags "-X main.version=$(VERSION)" -o $(BINARY) .

install: build
	mkdir -p $(PLUGIN_DIR)
	cp $(BINARY) $(PLUGIN_DIR)/$(BINARY)_v$(VERSION)

fmt:
	gofmt -s -w .
	terraform fmt -recursive examples

vet:
	go vet ./...

# Runs the Terraform CLI against the in-memory mock API (needs terraform on PATH
# or downloads it).
test:
	go test -count=1 ./...

# Creates REAL resources. Needs HIOK_ENDPOINT and HIOK_TOKEN or HIOK_EMAIL/HIOK_PASSWORD.
testacc:
	TF_ACC=1 go test -count=1 -v -timeout 60m ./internal/provider -run TestAccLive

# Serves the mock API on 127.0.0.1:18080 for trying the provider locally.
demo:
	go run ./internal/mockapi/cmd/mockhiok -addr 127.0.0.1:18080

clean:
	rm -f $(BINARY)
