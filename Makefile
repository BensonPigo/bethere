ifeq ($(origin VERSION), undefined)
VERSION := $(shell git describe --always --dirty)-$(shell date -u +%Y%m%d%H%M%S)
endif
DIST    := build/release/$(VERSION)

.PHONY: web test build release run

web:
	cd web && npm ci --no-fund --no-audit && npm run build
	touch web/dist/.gitkeep

test:
	go test ./...

build: web
	go build -ldflags "-X main.version=$(VERSION)" -o build/bethere ./cmd/bethere

run: build
	./build/bethere serve

# Linux binary for the GCE VM (tech-stack.md section 3, "發行版").
release: web test
	mkdir -p $(DIST)
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=$(VERSION)" -o $(DIST)/bethere ./cmd/bethere
	@echo $(VERSION) > build/release/LATEST
	@echo "release $(VERSION) -> $(DIST)"
