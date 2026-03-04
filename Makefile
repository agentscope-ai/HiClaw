# ============================================================
# HiClaw Makefile
# ============================================================
# Unified build, test, and release interface.
# Used locally and in CI/CD (GitHub Actions).
#
# Usage:
#   make build                    # Build all images (native arch, local)
#   make build-manager            # Build Manager image only
#   make build-worker             # Build Worker image only
#   make test                     # Build + run all integration tests
#   make test SKIP_BUILD=1        # Run tests without rebuilding
#   make test TEST_FILTER="01 02" # Run specific tests
#   make push                     # Build + push multi-arch images (amd64 + arm64)
#   make push-native              # Push native-arch images only (dev use, NOT recommended for registry)
#   make clean                    # Remove local images and test containers
# ============================================================

# ---------- Configuration ----------

VERSION        ?= latest
REGISTRY       ?= higress-registry.cn-hangzhou.cr.aliyuncs.com
REPO           ?= higress

MANAGER_IMAGE       ?= $(REGISTRY)/$(REPO)/hiclaw-manager
WORKER_IMAGE        ?= $(REGISTRY)/$(REPO)/hiclaw-worker
OPENCLAW_BASE_IMAGE ?= $(REGISTRY)/$(REPO)/openclaw-base

MANAGER_TAG    ?= $(MANAGER_IMAGE):$(VERSION)
WORKER_TAG     ?= $(WORKER_IMAGE):$(VERSION)
OPENCLAW_BASE_TAG ?= $(OPENCLAW_BASE_IMAGE):$(VERSION)

# Local image names (no registry prefix, used by tests and install script)
LOCAL_MANAGER       = hiclaw/manager-agent:$(VERSION)
LOCAL_WORKER        = hiclaw/worker-agent:$(VERSION)
LOCAL_OPENCLAW_BASE = hiclaw/openclaw-base:$(VERSION)

# Higress base image registry (regional mirrors auto-synced from cn-hangzhou primary)
#   China (default): higress-registry.cn-hangzhou.cr.aliyuncs.com
#   North America:   higress-registry.us-west-1.cr.aliyuncs.com
#   Southeast Asia:  higress-registry.ap-southeast-7.cr.aliyuncs.com
HIGRESS_REGISTRY  ?= higress-registry.cn-hangzhou.cr.aliyuncs.com

# Build flags
DOCKER_BUILD_ARGS ?=
DOCKER_PLATFORM   ?=
# Makefile helper: comma literal for $(subst)
comma := ,

ifdef DOCKER_PLATFORM
  PLATFORM_FLAG = --platform $(DOCKER_PLATFORM)
else
  PLATFORM_FLAG =
endif

REGISTRY_ARG = --build-arg HIGRESS_REGISTRY=$(HIGRESS_REGISTRY)
BUILTIN_VERSION_ARG = --build-arg BUILTIN_VERSION=$(VERSION)

# Multi-arch build configuration
# Platforms for multi-arch builds (comma-separated, no spaces)
MULTIARCH_PLATFORMS ?= linux/amd64,linux/arm64
# Buildx builder name (auto-created if not exists)
BUILDX_BUILDER     ?= hiclaw-multiarch

# Test flags
SKIP_BUILD     ?=
TEST_FILTER    ?=

# ---------- Phony targets ----------

.PHONY: all build build-openclaw-base build-manager build-worker \
        tag push push-openclaw-base push-manager push-worker \
        push-native push-native-manager push-native-worker \
        buildx-setup \
        test test-quick test-installed \
        install uninstall replay replay-log \
        mirror-images clean help

# ---------- Default ----------

all: build

# ---------- Build ----------

build: build-manager build-worker ## Build all images (base image pulled from registry, not rebuilt locally)

build-openclaw-base: ## Build OpenClaw base image
	@echo "==> Building OpenClaw base image: $(LOCAL_OPENCLAW_BASE) (registry: $(HIGRESS_REGISTRY))"
	docker build $(PLATFORM_FLAG) $(REGISTRY_ARG) $(DOCKER_BUILD_ARGS) \
		-t $(LOCAL_OPENCLAW_BASE) \
		./openclaw-base/

# build targets use the locally-built openclaw-base; push targets use the registry image
# Both build and push targets use the registry :latest base image for layer sharing
OPENCLAW_BASE_BUILD_ARG = --build-arg OPENCLAW_BASE_IMAGE=$(OPENCLAW_BASE_IMAGE):latest
# push targets always use :latest so manager/worker layers are shared across releases
OPENCLAW_BASE_PUSH_ARG  = --build-arg OPENCLAW_BASE_IMAGE=$(OPENCLAW_BASE_IMAGE):latest

build-manager: ## Build Manager image
	@echo "==> Building Manager image: $(LOCAL_MANAGER) (registry: $(HIGRESS_REGISTRY))"
	docker build $(PLATFORM_FLAG) $(REGISTRY_ARG) $(BUILTIN_VERSION_ARG) $(OPENCLAW_BASE_BUILD_ARG) $(DOCKER_BUILD_ARGS) \
		-t $(LOCAL_MANAGER) \
		./manager/

build-worker: ## Build Worker image
	@echo "==> Building Worker image: $(LOCAL_WORKER) (registry: $(HIGRESS_REGISTRY))"
	docker build $(PLATFORM_FLAG) $(REGISTRY_ARG) $(OPENCLAW_BASE_BUILD_ARG) $(DOCKER_BUILD_ARGS) \
		-t $(LOCAL_WORKER) \
		./worker/

# ---------- Tag ----------

tag: build ## Tag images for registry push
	docker tag $(LOCAL_MANAGER) $(MANAGER_TAG)
	docker tag $(LOCAL_WORKER) $(WORKER_TAG)
ifeq ($(VERSION),latest)
	@echo "==> Images tagged as $(VERSION)"
else
	docker tag $(LOCAL_MANAGER) $(MANAGER_IMAGE):latest
	docker tag $(LOCAL_WORKER) $(WORKER_IMAGE):latest
	@echo "==> Images tagged as $(VERSION) and latest"
endif

# ---------- Push (multi-arch, default) ----------
# Default push always builds multi-arch manifests to avoid overwriting
# existing multi-arch images with a single-arch image.
# Automatically detects Docker vs Podman and uses the appropriate strategy:
#   Docker  -> docker buildx build --platform ... --push
#   Podman  -> podman build --platform X --manifest M (per-platform) + manifest push

# Runtime detection (works even when podman is aliased as docker)
IS_PODMAN := $(shell docker version 2>&1 | grep -qi podman && echo 1 || echo 0)

buildx-setup: ## Ensure multi-arch build prerequisites are met
ifeq ($(IS_PODMAN),1)
	@echo "==> Podman detected — no buildx setup needed (using manifest workflow)"
else
	@if ! docker buildx inspect $(BUILDX_BUILDER) >/dev/null 2>&1; then \
		echo "==> Creating buildx builder: $(BUILDX_BUILDER)"; \
		docker buildx create --name $(BUILDX_BUILDER) --driver docker-container --bootstrap; \
	else \
		echo "==> Buildx builder $(BUILDX_BUILDER) already exists"; \
	fi
endif

push: push-manager push-worker ## Build + push multi-arch images (amd64 + arm64); base image built separately via build-base.yml

push-openclaw-base: buildx-setup ## Build + push multi-arch OpenClaw base image
	@echo "==> Building + pushing multi-arch OpenClaw base: $(OPENCLAW_BASE_TAG) [$(MULTIARCH_PLATFORMS)]"
ifeq ($(IS_PODMAN),1)
	@# Podman: build each platform into a manifest list, then push
	-podman manifest rm $(OPENCLAW_BASE_TAG) 2>/dev/null
	$(foreach plat,$(subst $(comma), ,$(MULTIARCH_PLATFORMS)), \
		echo "  -> Building OpenClaw base for $(plat)..." && \
		podman build --platform $(plat) \
			$(REGISTRY_ARG) $(DOCKER_BUILD_ARGS) \
			--manifest $(OPENCLAW_BASE_TAG) \
			./openclaw-base/ && ) true
	podman manifest push --all $(OPENCLAW_BASE_TAG) docker://$(OPENCLAW_BASE_TAG)
	$(if $(filter-out latest,$(VERSION)), \
		podman manifest push --all $(OPENCLAW_BASE_TAG) docker://$(OPENCLAW_BASE_IMAGE):latest)
else
	docker buildx build \
		--builder $(BUILDX_BUILDER) \
		--platform $(MULTIARCH_PLATFORMS) \
		$(REGISTRY_ARG) $(DOCKER_BUILD_ARGS) \
		-t $(OPENCLAW_BASE_TAG) \
		$(if $(filter-out latest,$(VERSION)),-t $(OPENCLAW_BASE_IMAGE):latest) \
		--push \
		./openclaw-base/
endif

push-manager: buildx-setup ## Build + push multi-arch Manager image
	@echo "==> Building + pushing multi-arch Manager: $(MANAGER_TAG) [$(MULTIARCH_PLATFORMS)]"
ifeq ($(IS_PODMAN),1)
	@# Podman: build each platform into a manifest list, then push
	-podman manifest rm $(MANAGER_TAG) 2>/dev/null
	$(foreach plat,$(subst $(comma), ,$(MULTIARCH_PLATFORMS)), \
		echo "  -> Building Manager for $(plat)..." && \
		podman build --platform $(plat) \
			$(REGISTRY_ARG) $(BUILTIN_VERSION_ARG) $(OPENCLAW_BASE_PUSH_ARG) $(DOCKER_BUILD_ARGS) \
			--manifest $(MANAGER_TAG) \
			./manager/ && ) true
	podman manifest push --all $(MANAGER_TAG) docker://$(MANAGER_TAG)
	$(if $(filter-out latest,$(VERSION)), \
		podman manifest push --all $(MANAGER_TAG) docker://$(MANAGER_IMAGE):latest)
else
	docker buildx build \
		--builder $(BUILDX_BUILDER) \
		--platform $(MULTIARCH_PLATFORMS) \
		$(REGISTRY_ARG) $(BUILTIN_VERSION_ARG) $(OPENCLAW_BASE_PUSH_ARG) $(DOCKER_BUILD_ARGS) \
		-t $(MANAGER_TAG) \
		$(if $(filter-out latest,$(VERSION)),-t $(MANAGER_IMAGE):latest) \
		--push \
		./manager/
endif

push-worker: buildx-setup ## Build + push multi-arch Worker image
	@echo "==> Building + pushing multi-arch Worker: $(WORKER_TAG) [$(MULTIARCH_PLATFORMS)]"
ifeq ($(IS_PODMAN),1)
	@# Podman: build each platform into a manifest list, then push
	-podman manifest rm $(WORKER_TAG) 2>/dev/null
	$(foreach plat,$(subst $(comma), ,$(MULTIARCH_PLATFORMS)), \
		echo "  -> Building Worker for $(plat)..." && \
		podman build --platform $(plat) \
			$(REGISTRY_ARG) $(OPENCLAW_BASE_PUSH_ARG) $(DOCKER_BUILD_ARGS) \
			--manifest $(WORKER_TAG) \
			./worker/ && ) true
	podman manifest push --all $(WORKER_TAG) docker://$(WORKER_TAG)
	$(if $(filter-out latest,$(VERSION)), \
		podman manifest push --all $(WORKER_TAG) docker://$(WORKER_IMAGE):latest)
else
	docker buildx build \
		--builder $(BUILDX_BUILDER) \
		--platform $(MULTIARCH_PLATFORMS) \
		$(REGISTRY_ARG) $(OPENCLAW_BASE_PUSH_ARG) $(DOCKER_BUILD_ARGS) \
		-t $(WORKER_TAG) \
		$(if $(filter-out latest,$(VERSION)),-t $(WORKER_IMAGE):latest) \
		--push \
		./worker/
endif

# ---------- Push native-arch only (dev use) ----------
# WARNING: Pushing single-arch images will overwrite multi-arch manifests.
# Only use for local development / testing, never for release.

push-native: tag ## Push native-arch images (dev only, overwrites multi-arch!)
	@echo "WARNING: Pushing native-arch only — this overwrites multi-arch manifests!"
	@echo "==> Pushing Manager: $(MANAGER_TAG)"
	docker push $(MANAGER_TAG)
	@echo "==> Pushing Worker: $(WORKER_TAG)"
	docker push $(WORKER_TAG)
ifneq ($(VERSION),latest)
	docker push $(MANAGER_IMAGE):latest
	docker push $(WORKER_IMAGE):latest
endif

push-native-manager: build-manager ## Push native-arch Manager only (dev)
	docker tag $(LOCAL_MANAGER) $(MANAGER_TAG)
	docker push $(MANAGER_TAG)

push-native-worker: build-worker ## Push native-arch Worker only (dev)
	docker tag $(LOCAL_WORKER) $(WORKER_TAG)
	docker push $(WORKER_TAG)

# ---------- Test ----------

# Wait for Manager services to be ready (used internally by test target)
# Uses docker exec to check health inside container (works regardless of port mappings)
# Usage: make wait-ready [CONTAINER=name]
.PHONY: wait-ready
wait-ready:
	@echo "==> Waiting for Manager services to be ready (container: $(or $(CONTAINER),hiclaw-manager))..."
	@TIMEOUT=300; ELAPSED=0; \
	while [ "$$ELAPSED" -lt "$$TIMEOUT" ]; do \
		RESULT=$$(docker exec $(or $(CONTAINER),hiclaw-manager) bash -c 'curl -s -o /dev/null -w "%{http_code} " "http://127.0.0.1:6167/_matrix/client/versions" 2>/dev/null || echo "000 "; curl -s -o /dev/null -w "%{http_code} " "http://127.0.0.1:9000/minio/health/live" 2>/dev/null || echo "000 "; curl -s -o /dev/null -w "%{http_code}" "http://127.0.0.1:8001/" 2>/dev/null || echo "000"' 2>/dev/null); \
		MATRIX=$$(echo "$$RESULT" | tr -d '\n' | cut -d' ' -f1); \
		MINIO=$$(echo "$$RESULT" | tr -d '\n' | cut -d' ' -f2); \
		CONSOLE=$$(echo "$$RESULT" | tr -d '\n' | cut -d' ' -f3); \
		if [ "$$MATRIX" = "200" ] && [ "$$MINIO" = "200" ] && [ "$$CONSOLE" = "200" ]; then \
			echo "==> Services ready (took $${ELAPSED}s)"; \
			echo "==> Waiting 60s for Manager Agent initialization..."; \
			sleep 60; \
			echo "==> Manager Agent should be ready now"; \
			exit 0; \
		fi; \
		sleep 5; \
		ELAPSED=$$((ELAPSED + 5)); \
		echo "    Still waiting... ($${ELAPSED}s) Matrix=$$MATRIX MinIO=$$MINIO Console=$$CONSOLE"; \
	done; \
	echo "ERROR: Manager did not become ready within $${TIMEOUT}s"; \
	exit 1

test: ## Run integration tests (creates test container)
ifdef SKIP_INSTALL
	@echo "==> Running tests against existing installation"
	@docker exec hiclaw-manager touch /root/manager-workspace/yolo-mode 2>/dev/null || true
	./tests/run-all-tests.sh --skip-build --use-existing $(if $(TEST_FILTER),--test-filter "$(TEST_FILTER)")
else
	@echo "==> Installing test Manager and running tests"
	$(MAKE) uninstall 2>/dev/null || true
	HICLAW_YOLO=1 $(MAKE) install
	$(MAKE) wait-ready
	./tests/run-all-tests.sh --skip-build --use-existing $(if $(TEST_FILTER),--test-filter "$(TEST_FILTER)")
endif

test-quick: ## Run test-01 only (quick smoke test)
	$(MAKE) test TEST_FILTER="01"

test-installed: ## Run tests against an already-installed Manager (no container lifecycle)
	./tests/run-all-tests.sh --skip-build --use-existing $(if $(TEST_FILTER),--test-filter "$(TEST_FILTER)")

# ---------- Install / Uninstall ----------

install: ## Install Manager locally (non-interactive, set HICLAW_LLM_API_KEY)
ifndef SKIP_BUILD
	$(MAKE) build
endif
	@echo "==> Installing HiClaw Manager (non-interactive)..."
	HICLAW_NON_INTERACTIVE=1 HICLAW_VERSION=$(VERSION) HICLAW_MOUNT_SOCKET=1 \
		HICLAW_INSTALL_MANAGER_IMAGE=$(LOCAL_MANAGER) \
		HICLAW_INSTALL_WORKER_IMAGE=$(LOCAL_WORKER) \
		./install/hiclaw-install.sh manager

install-interactive: ## Install Manager interactively (prompts for config)
ifndef SKIP_BUILD
	$(MAKE) build
endif
	@echo "==> Installing HiClaw Manager (interactive)..."
	HICLAW_VERSION=$(VERSION) HICLAW_MOUNT_SOCKET=1 \
		HICLAW_INSTALL_MANAGER_IMAGE=$(LOCAL_MANAGER) \
		HICLAW_INSTALL_WORKER_IMAGE=$(LOCAL_WORKER) \
		./install/hiclaw-install.sh manager

uninstall: ## Stop and remove Manager + all Worker containers
	@echo "==> Uninstalling HiClaw..."
	-docker stop hiclaw-manager 2>/dev/null && docker rm hiclaw-manager 2>/dev/null || true
	@for c in $$(docker ps -a --filter "name=hiclaw-worker-" --format '{{.Names}}' 2>/dev/null); do \
		echo "  Removing Worker: $$c"; \
		docker rm -f "$$c" 2>/dev/null || true; \
	done
	-docker volume rm hiclaw-data 2>/dev/null && echo "  Removed volume: hiclaw-data" || true
	@if [ -f ./hiclaw-manager.env ]; then \
		DATA_DIR=$$(grep '^HICLAW_DATA_DIR=' ./hiclaw-manager.env 2>/dev/null | cut -d= -f2-); \
		if [ -n "$$DATA_DIR" ] && [ -d "$$DATA_DIR" ]; then \
			echo "  External data directory preserved: $$DATA_DIR"; \
			echo "  To delete: rm -rf $$DATA_DIR"; \
		fi; \
		WORKSPACE_DIR=$$(grep '^HICLAW_WORKSPACE_DIR=' ./hiclaw-manager.env 2>/dev/null | cut -d= -f2-); \
		if [ -n "$$WORKSPACE_DIR" ] && [ -d "$$WORKSPACE_DIR" ]; then \
			PARENT=$$(dirname "$$WORKSPACE_DIR"); \
			BASE=$$(basename "$$WORKSPACE_DIR"); \
			docker run --rm --entrypoint sh -v "$$PARENT:/host-parent" $(LOCAL_MANAGER) -c "rm -rf /host-parent/$$BASE"; \
			echo "  Removed: $$WORKSPACE_DIR"; \
		fi; \
	fi
	@echo "==> HiClaw uninstalled"

# ---------- Replay ----------

replay: ## Send a task to Manager (TASK="..." or interactive, YOLO mode auto-enabled)
	@docker exec hiclaw-manager touch /root/manager-workspace/yolo-mode 2>/dev/null || true
ifdef TASK
	REPLAY_USE_DOCKER_EXEC=1 ./scripts/replay-task.sh "$(TASK)"
else
	REPLAY_USE_DOCKER_EXEC=1 ./scripts/replay-task.sh
endif

replay-log: ## View the latest replay conversation log
	@LATEST=$$(ls -t logs/replay/replay-*.log 2>/dev/null | head -1); \
	if [ -z "$$LATEST" ]; then \
		echo "No replay logs found. Run 'make replay' first."; \
	else \
		echo "==> Latest log: $$LATEST"; \
		echo ""; \
		cat "$$LATEST"; \
	fi

# ---------- Mirror upstream images ----------

mirror-images: ## Mirror upstream images to Higress registry (multi-arch, via skopeo)
	./hack/mirror-images.sh

# ---------- Clean ----------

clean: ## Remove local images and test containers
	@echo "==> Stopping and removing test containers..."
	-docker stop $(TEST_CONTAINER) 2>/dev/null
	-docker rm $(TEST_CONTAINER) 2>/dev/null
	-docker ps -a --filter "name=hiclaw-test-worker-" --format '{{.Names}}' | xargs -r docker rm -f 2>/dev/null
	@echo "==> Removing local images..."
	-docker rmi $(LOCAL_MANAGER) 2>/dev/null
	-docker rmi $(LOCAL_WORKER) 2>/dev/null
	-docker rmi $(LOCAL_OPENCLAW_BASE) 2>/dev/null
	@echo "==> Clean complete"

# ---------- Help ----------

help: ## Show this help
	@echo "HiClaw Makefile targets:"
	@echo ""
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-18s\033[0m %s\n", $$1, $$2}'
	@echo ""
	@echo "Variables:"
	@echo "  VERSION              Image tag             (default: latest)"
	@echo "  REGISTRY             Container registry    (default: higress-registry.cn-hangzhou.cr.aliyuncs.com)"
	@echo "  REPO                 Repository path       (default: higress/hiclaw)"
	@echo "  HIGRESS_REGISTRY     Base image registry   (default: cn-hangzhou, see below)"
	@echo "  SKIP_BUILD           Skip build in 'install' (set to 1 to skip)"
	@echo "  SKIP_INSTALL         Skip install in 'test' (set to 1 to test existing)"
	@echo "  TEST_FILTER          Test numbers to run   (e.g., '01 02 03')"
	@echo "  TEST_CONTAINER       Test container name   (default: hiclaw-manager-test)"
	@echo "  DOCKER_PLATFORM      Build platform        (e.g., linux/amd64)"
	@echo "  MULTIARCH_PLATFORMS  Multi-arch platforms   (default: linux/amd64,linux/arm64)"
	@echo "  BUILDX_BUILDER       Buildx builder name   (default: hiclaw-multiarch)"
	@echo ""
	@echo "HIGRESS_REGISTRY regions (mirrors auto-synced from cn-hangzhou):"
	@echo "  China (default):  higress-registry.cn-hangzhou.cr.aliyuncs.com"
	@echo "  North America:    higress-registry.us-west-1.cr.aliyuncs.com"
	@echo "  Southeast Asia:   higress-registry.ap-southeast-7.cr.aliyuncs.com"
	@echo ""
	@echo "Push (multi-arch by default):"
	@echo "  make push VERSION=0.1.0             # Build amd64+arm64 and push"
	@echo "  make push MULTIARCH_PLATFORMS=linux/amd64,linux/arm64,linux/arm/v7"
	@echo "  make push-native VERSION=dev        # Push native-arch only (dev, overwrites multi-arch!)"
	@echo ""
	@echo "Install / Uninstall / Replay:"
	@echo "  HICLAW_LLM_API_KEY=sk-xxx make install          # Build + install Manager (non-interactive)"
	@echo "  HICLAW_LLM_API_KEY=sk-xxx HICLAW_DATA_DIR=~/hiclaw-data make install  # With external data dir"
	@echo "  make uninstall                                  # Stop + remove Manager and Workers"
	@echo ""
	@echo "Test:"
	@echo "  HICLAW_LLM_API_KEY=sk-xxx make test             # Install + run all tests (auto cleanup)"
	@echo "  make test SKIP_BUILD=1                          # Run tests without rebuilding"
	@echo "  make test TEST_FILTER=\"01 02\"                   # Run specific tests only"
	@echo "  make test SKIP_INSTALL=1                        # Run tests against existing Manager"
	@echo "  make test TEST_CONTAINER=my-test                # Use custom container name"
	@echo "  make replay TASK=\"Create worker alice\"          # Send a task to Manager"
	@echo "  make replay                                     # Interactive task input"
	@echo ""
	@echo "Mirror variables (for 'make mirror-images'):"
	@echo "  DATE_TAG         Tag for date-pinned images  (default: YYYYMMDD)"
	@echo "  DRY_RUN          Show commands only           (set to 1)"
	@echo "  USE_CONTAINER    Use skopeo container         (set to 1)"
