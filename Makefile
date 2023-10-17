GO_GCFLAGS ?= -gcflags=all='-N -l'
GO=GO111MODULE=on go
GO_BUILD_RECIPE=CGO_ENABLED=0 $(GO) build $(GO_GCFLAGS)

OUT_DIR ?= bin

all: build

.PHONY: build
build:
	$(GO_BUILD_RECIPE) -o $(OUT_DIR)/hypershift-agent-automation .

clean:
	rm -rf $(OUT_DIR)/*
