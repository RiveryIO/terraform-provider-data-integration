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

# Regenerate registry docs from schema (requires tfplugindocs on PATH).
docs:
	tfplugindocs generate --provider-name boomi_data_integration
