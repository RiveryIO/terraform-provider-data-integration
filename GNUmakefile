PROVIDER := terraform-provider-data-integration
VERSION  ?= dev

.PHONY: build install test testacc fmt vet lint docs tidy

build:
	go build -ldflags "-X main.version=$(VERSION)" -o bin/$(PROVIDER) .

install:
	go install -ldflags "-X main.version=$(VERSION)" .

# Unit tests (fast). Acceptance tests auto-skip without TF_ACC.
test:
	go test ./... -count=1

# Live acceptance tests — requires integration creds:
#   DATA_INTEGRATION_API_TOKEN / _ACCOUNT_ID / _ENVIRONMENT_ID
# and RIVERY_ACC_SUBRIVER_ID for the data-flow logic leaf step.
testacc:
	TF_ACC=1 go test ./internal/provider/ -run TestAcc -v -timeout 30m

fmt:
	gofmt -w .

vet:
	go vet ./...

tidy:
	go mod tidy

# Regenerate registry docs from schema.
# PIN both tools to match CI (test.yml) exactly — docs differ by tfplugindocs and
# Terraform version (write-only markers require Terraform >= 1.11). Diverging
# versions produce a docs-drift failure on every PR.
#
# Requires Terraform 1.11.4 on PATH. Install once:
#   curl -fsSL https://releases.hashicorp.com/terraform/1.11.4/terraform_1.11.4_darwin_arm64.zip \
#     | tar -xz -C /usr/local/bin terraform
# (adjust OS/arch as needed; linux_amd64 on CI)
docs:
	go install github.com/hashicorp/terraform-plugin-docs/cmd/tfplugindocs@v0.21.0
	tfplugindocs generate --provider-name boomi
