# SDK generation and validation targets
.PHONY: validate generate-all generate-ts generate-python generate-go generate-java clean

SPEC := openapi.yaml

# Validate the OpenAPI spec using go-based validator (no npm required)
validate:
	@echo "Validating OpenAPI specification..."
	@go run ./validate/main.go $(SPEC)
	@echo "✓ OpenAPI spec is valid"

# Generate all SDKs (placeholder — implemented in US-14.3 through US-14.6)
generate-all: validate generate-ts generate-python generate-go generate-java

generate-ts: validate
	@echo "TypeScript SDK generation not yet implemented (US-14.3)"

generate-python: validate
	@echo "Python SDK generation not yet implemented (US-14.4)"

generate-go: validate
	@echo "Go SDK generation not yet implemented (US-14.5)"

generate-java: validate
	@echo "Java SDK generation not yet implemented (US-14.6)"

clean:
	rm -rf typescript/ python/ go/ java/
