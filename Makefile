.PHONY: build test coverage lint ci clean

BINARY      := .bin/b
MODULE      := github.com/fentas/b
MAIN        := ./cmd/b
COVER_DIR   := test/coverage
COVER_FILE  := $(COVER_DIR)/coverage.out
COVER_JSON  := $(COVER_DIR)/coverage.json
COVER_BADGE := $(COVER_DIR)/badge.json

# Build flags (match goreleaser)
LDFLAGS := -w

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o $(BINARY) $(MAIN)

test:
	go test -race ./...

coverage: $(COVER_DIR)
	go test -race -coverprofile="$(COVER_FILE)" ./...
	go tool cover -func="$(COVER_FILE)"
	@echo ""
	@echo "--- Per-package summary ---"
	@go tool cover -func="$(COVER_FILE)" | tail -1
	@# Generate JSON coverage report
	@go tool cover -func="$(COVER_FILE)" | \
		awk 'BEGIN{print "["} \
		NR>1 && $$NF ~ /%$$/ { \
			gsub(/%/,"",$$NF); \
			if(NR>2) printf ",\n"; \
			printf "  {\"file\": \"%s\", \"function\": \"%s\", \"coverage\": %s}", $$1, $$2, $$NF \
		} \
		END{print "\n]"}' > "$(COVER_JSON)"
	@# Generate shields.io endpoint badge
	@go tool cover -func="$(COVER_FILE)" | tail -1 | \
		awk '{ \
			gsub(/%/,"",$$NF); pct=$$NF+0; \
			if      (pct>=90) c="brightgreen"; \
			else if (pct>=80) c="green"; \
			else if (pct>=70) c="yellowgreen"; \
			else if (pct>=50) c="yellow"; \
			else if (pct>=30) c="orange"; \
			else              c="red"; \
			printf "{\"schemaVersion\":1,\"label\":\"coverage\",\"message\":\"%.1f%%\",\"color\":\"%s\"}\n", pct, c \
		}' > "$(COVER_BADGE)"
	@echo "Coverage report: $(COVER_FILE)"
	@echo "Coverage JSON:   $(COVER_JSON)"
	@echo "Coverage badge:  $(COVER_BADGE)"

$(COVER_DIR):
	mkdir -p $(COVER_DIR)

lint:
	PATH="$(CURDIR)/.bin:$(PATH)" golangci-lint run ./...

ci: lint test coverage
	@echo "CI checks passed"

clean:
	rm -f $(BINARY)
	rm -rf $(COVER_DIR)
