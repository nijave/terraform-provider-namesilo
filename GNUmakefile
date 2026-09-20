default: test

.PHONY: build
build:
	go build -o dist/ ./...

.PHONY: test
test:
	@if command -v tofu >/dev/null 2>&1; then \
		TF_ACC_TERRAFORM_PATH="$$(command -v tofu)" TF_ACC_PROVIDER_HOST=registry.opentofu.org \
			go test ./... -timeout 20m; \
	else \
		go test ./... -timeout 20m; \
	fi

.PHONY: testacc-live
testacc-live:
	@command -v tofu >/dev/null || (echo "tofu not found in PATH; OpenTofu >= 1.10 is required" && exit 1)
	TF_ACC=1 TF_ACC_TERRAFORM_PATH="$$(command -v tofu)" TF_ACC_PROVIDER_HOST=registry.opentofu.org \
		go test ./internal/provider/ -run 'TestAccLive' -v $(TESTARGS) -timeout 60m

.PHONY: fmt
fmt:
	gofmt -w -l .

.PHONY: vet
vet:
	go vet ./...

.PHONY: release
release:
	@test $${RELEASE_VERSION?Please set environment variable RELEASE_VERSION}
	@git tag $$RELEASE_VERSION
	@git push origin $$RELEASE_VERSION

.PHONY: docs
docs:
	./tools/gen-schema.sh
	cd tools && go generate ./...
