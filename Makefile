BINARY  := terraform-provider-hiok
VERSION ?= 0.1.0
# Where Terraform looks for locally-built providers.
PLUGIN_DIR := $(HOME)/.terraform.d/plugins/registry.terraform.io/HIOK-Official/hiok/$(VERSION)/linux_amd64

.PHONY: build install fmt vet test clean

build:
	go build -o $(BINARY) .

install: build
	mkdir -p $(PLUGIN_DIR)
	cp $(BINARY) $(PLUGIN_DIR)/$(BINARY)_v$(VERSION)

fmt:
	gofmt -s -w .

vet:
	go vet ./...

test:
	go test ./...

clean:
	rm -f $(BINARY)
