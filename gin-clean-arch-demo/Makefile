# Documentation and project tasks.
#
#   make docs    regenerate the Swagger specification from handler annotations
#
# `swag` is a tool dependency in go.mod, so `go tool swag` runs the pinned
# version without installing anything. The generated directory is committed:
# the binary embeds it, so a deploy needs no generation step.

SWAG := go tool swag
# Parsing is limited to the directories that carry annotations. Parsing all of
# "./" also works but warns about directories with no Go files.
SWAG_DIRS := ./cmd/server,./internal/adapter/httpapi/handlers
SWAG_ARGS := init -d $(SWAG_DIRS) -g main.go --parseInternal
DOCS_OUT  := internal/adapter/httpapi/docs

.PHONY: help docs docs-check

help: ## List the available targets
	@grep -h -E '^[a-z-]+:.*?## ' $(MAKEFILE_LIST) \
		| awk 'BEGIN {FS = ":.*?## "}; {printf "  %-12s %s\n", $$1, $$2}'

docs: ## Regenerate the Swagger spec and UI assets from annotations
	@$(SWAG) $(SWAG_ARGS) -o $(DOCS_OUT)
	@echo "generated $(DOCS_OUT)"

# Verifies the committed output matches the annotations, so CI fails when a
# handler changes without `make docs`. Generation happens in a scratch
# directory, so a failed check leaves the working copy untouched. The scratch
# path keeps the `docs` basename because swag derives the package name from it.
docs-check: ## Fail if the committed spec is stale relative to the annotations
	@tmp=$$(mktemp -d) && trap 'rm -rf "$$tmp"' EXIT && \
		$(SWAG) $(SWAG_ARGS) -o "$$tmp/docs" >/dev/null 2>&1 && \
		gofmt -w "$$tmp/docs" && \
		if ! diff -r "$$tmp/docs" $(DOCS_OUT) >/dev/null; then \
			echo "spec is stale; run 'make docs' and commit the result" >&2; \
			diff -r "$$tmp/docs" $(DOCS_OUT) >&2 || true; \
			exit 1; \
		fi && \
		echo "spec is up to date"
