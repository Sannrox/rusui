# Makefile for Rusui.
# Wrapper around scripts/make-targets.

.EXPORT_ALL_VARIABLES:

WHAT ?=

.PHONY: all
# Build host-platform binaries into _output/local/bin.
# Example: make all
#          make all WHAT=cmd/rusui
all:
	./scripts/make-targets/build.sh $(WHAT)

.PHONY: validate
# Run scripts/validate-*.sh
# Example: make validate
validate:
	./scripts/make-targets/validate.sh

.PHONY: update
# Run scripts/update-*.sh
# Example: make update
update:
	./scripts/make-targets/update.sh

.PHONY: test
# Example: make test
#          make test GOFLAGS="-v" COVER=1
#          make test WHAT=./internal/engine GOFLAGS="-v" TEST_ARGS='-run ^TestClaim$$'
test:
	./scripts/make-targets/test.sh $(WHAT)

.PHONY: release-images
# Linux binaries in the docker build image, then rusui and rusui-runner images.
# Defaults to the Docker engine's linux arch. Override:
#   BUILD_PLATFORMS='linux/amd64 linux/arm64' make release-images
release-images:
	./build/release-images.sh

.PHONY: guest-image
# Build the reference guest image (git, gh, Node.js, claude-agent-acp).
# Example: make guest-image CONTAINER_RUNTIME=podman
guest-image:
	./scripts/make-targets/guest-image.sh

.PHONY: docker-clean
# Remove docker build/data containers, rusui build tags, and _output
docker-clean:
	./build/make-clean.sh

.PHONY: clean
# Remove build output
clean:
	rm -rf _output .version
