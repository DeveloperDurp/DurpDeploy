.PHONY: build build-agent build-all agent-container agent-container-contract agent-compose-contract agent-systemd-contract agent-documentation-contract agent-e2e-sqlite agent-e2e-postgres agent-e2e-mssql dev dev-server dev-postgres dev-mssql e2e-test e2e-test-isolated e2e-postgres e2e-mssql check-openssl templ-generate tailwind-build js-build npm-install golines golines-check clean test mfa-e2e-test auth-mfa-e2e-go-prepare auth-mfa-e2e-browser-prepare auth-mfa-e2e-sqlite-http auth-mfa-e2e-sqlite-browser auth-mfa-e2e-sqlite auth-mfa-e2e-postgres auth-mfa-e2e-mssql auth-mfa-e2e swagger-spec mobile-browser-container

BINARY_NAME=durpdeploy
MAIN_PATH=./cmd/server
AGENT_BINARY_NAME=durpdeploy-agent
AGENT_MAIN_PATH=./cmd/agent
AGENT_CONTAINER_IMAGE ?= durpdeploy-agent:local
DEV_POSTGRES_CONTAINER ?= durpdeploy-dev-postgres
DEV_POSTGRES_IMAGE ?= postgres:16-alpine
DEV_MSSQL_CONTAINER ?= durpdeploy-dev-mssql
DEV_MSSQL_IMAGE ?= mcr.microsoft.com/mssql/server:2022-latest
DEV_HTTPS_PROXY_CONTAINER ?= durpdeploy-dev-https
DEV_HTTPS_PROXY_PORT ?= 8443
DEV_HTTPS_PROXY_BACKEND ?= host.docker.internal:8080
# Browser HTTPS stays on 8443; agent TLS uses 8444 so the ports do not collide.
DEV_AGENT_LISTEN_ADDR ?= :8444
DEV_AGENT_PUBLIC_URL ?= https://localhost:8444
DEV_AGENT_IDENTITY_DIR ?= $(MAKEFILE_DIR).agent-identity

build: swagger-spec swagger-ui-copy templ-generate tailwind-build js-build
	go build -o $(BINARY_NAME) $(MAIN_PATH)

build-agent:
	go build -o $(AGENT_BINARY_NAME) $(AGENT_MAIN_PATH)

build-all: build build-agent

agent-container:
	docker build -f Dockerfile.agent -t $(AGENT_CONTAINER_IMAGE) .

agent-container-contract:
	AGENT_CONTAINER_IMAGE=$(AGENT_CONTAINER_IMAGE) bash ./scripts/check-agent-container-contract.sh

agent-compose-contract:
	bash ./scripts/check-agent-compose-contract.sh

agent-systemd-contract:
	bash ./scripts/check-agent-systemd-contract.sh

.PHONY: agent-runtime-e2e-postgres agent-runtime-e2e-mssql agent-runtime-e2e

agent-runtime-e2e-postgres:
	bash ./scripts/agent_runtime_db_parity.sh postgres

agent-runtime-e2e-mssql:
	bash ./scripts/agent_runtime_db_parity.sh mssql

agent-runtime-e2e: agent-runtime-e2e-postgres agent-runtime-e2e-mssql

agent-e2e-sqlite:
	bash ./scripts/agent_e2e_test.sh

agent-e2e-postgres:
	go test -count=1 -v ./cmd/server -run '^TestAgentListener_remoteAgentCompletesRemoteDispatch_Postgres$$'

agent-e2e-mssql:
	go test -count=1 -v ./cmd/server -run '^TestAgentListener_remoteAgentCompletesRemoteDispatch_MSSQL$$'

agent-documentation-contract:
	bash ./scripts/check-agent-documentation-contract.sh

# Hot-reload dev server. Watches .go/.templ/.sql in cmd, internal, views, migrations.
# Reads the shell-compatible DURPDEPLOY_SECRET_KEY assignment from ENV_FILE
# (default repository/.env), then uses inherited DURPDEPLOY_SECRET_KEY, then
# generates a throwaway key if needed. Other .env assignments stay in the
# subshell.
# ponytail: CSS/JS source changes need a separate `make tailwind-build && make js-build`
# and the air build to retrigger. Add a second air include_dir entry when that hurts.
MAKEFILE_DIR := $(dir $(abspath $(lastword $(MAKEFILE_LIST))))
ENV_FILE ?= $(MAKEFILE_DIR).env

dev: check-openssl
	mkdir -p '$(DEV_AGENT_IDENTITY_DIR)'
	DURPDEPLOY_HTTPS_PROXY_CONTAINER='$(DEV_HTTPS_PROXY_CONTAINER)' \
	DURPDEPLOY_HTTPS_PROXY_PORT='$(DEV_HTTPS_PROXY_PORT)' \
	DURPDEPLOY_HTTPS_PROXY_BACKEND='$(DEV_HTTPS_PROXY_BACKEND)' \
	DURPDEPLOY_AGENT_LISTEN_ADDR='$(DEV_AGENT_LISTEN_ADDR)' \
	DURPDEPLOY_AGENT_PUBLIC_URL='$(DEV_AGENT_PUBLIC_URL)' \
	DURPDEPLOY_AGENT_IDENTITY_DIR='$(DEV_AGENT_IDENTITY_DIR)' \
	DURPDEPLOY_URL="$${DURPDEPLOY_URL:-https://localhost:$(DEV_HTTPS_PROXY_PORT)}" \
	./scripts/dev_https_proxy.sh "$${MAKE:-make}" --no-print-directory dev-server

dev-server:
	env_secret_key=$$(unset DURPDEPLOY_SECRET_KEY; \
		if [ -f "$(ENV_FILE)" ]; then . "$(ENV_FILE)"; fi; \
		printf '%s' "$${DURPDEPLOY_SECRET_KEY:-}"); \
	if [ -n "$$env_secret_key" ]; then DURPDEPLOY_SECRET_KEY="$$env_secret_key"; fi; \
	if [ -f "$(ENV_FILE)" ]; then . "$(ENV_FILE)"; fi; \
	if [ "$${DURPDEPLOY_URL+x}" != "" ]; then export DURPDEPLOY_URL; fi; \
	if [ "$${DURPDEPLOY_OIDC_ISSUER+x}" != "" ]; then export DURPDEPLOY_OIDC_ISSUER; fi; \
	if [ "$${DURPDEPLOY_OIDC_CLIENT_ID+x}" != "" ]; then export DURPDEPLOY_OIDC_CLIENT_ID; fi; \
	if [ "$${DURPDEPLOY_OIDC_CLIENT_SECRET+x}" != "" ]; then export DURPDEPLOY_OIDC_CLIENT_SECRET; fi; \
	if [ "$${DURPDEPLOY_OIDC_ADMIN_GROUP+x}" != "" ]; then export DURPDEPLOY_OIDC_ADMIN_GROUP; fi; \
	if [ "$${DURPDEPLOY_OIDC_DEPLOYER_GROUP+x}" != "" ]; then export DURPDEPLOY_OIDC_DEPLOYER_GROUP; fi; \
	if [ "$${DURPDEPLOY_OIDC_VIEWER_GROUP+x}" != "" ]; then export DURPDEPLOY_OIDC_VIEWER_GROUP; fi; \
	if [ "$${DURPDEPLOY_OIDC_DISPLAY_NAME+x}" != "" ]; then export DURPDEPLOY_OIDC_DISPLAY_NAME; fi; \
	if [ "$${DURPDEPLOY_OIDC_GROUP_CLAIM+x}" != "" ]; then export DURPDEPLOY_OIDC_GROUP_CLAIM; fi; \
	if [ "$${DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED+x}" != "" ]; then export DURPDEPLOY_OIDC_REQUIRE_EMAIL_VERIFIED; fi; \
	DURPDEPLOY_SECRET_KEY=$${DURPDEPLOY_SECRET_KEY:-$$(openssl rand -base64 32)} \
	DURPDEPLOY_ENV_FILE="$(ENV_FILE)" go run github.com/air-verse/air@latest

# Disposable database containers for manual backend testing. Stop them with
# `docker stop $(DEV_POSTGRES_CONTAINER)` or `docker stop $(DEV_MSSQL_CONTAINER)`.
dev-postgres:
	@printf '%s\n' \
		'Database container: $(DEV_POSTGRES_CONTAINER)' \
		'DURPDEPLOY_DB=postgres://durpdeploy:durpdeploy@localhost:5432/durpdeploy?sslmode=disable'
	@if docker container inspect $(DEV_POSTGRES_CONTAINER) >/dev/null 2>&1; then \
		if [ "$$(docker inspect -f '{{.State.Running}}' $(DEV_POSTGRES_CONTAINER))" = true ]; then \
			echo 'Reusing running PostgreSQL container.'; \
		else \
			echo 'Starting existing PostgreSQL container.'; \
			docker start $(DEV_POSTGRES_CONTAINER) >/dev/null; \
		fi; \
	else \
		docker run -d --name $(DEV_POSTGRES_CONTAINER) \
			-e POSTGRES_USER=durpdeploy \
			-e POSTGRES_PASSWORD=durpdeploy \
			-e POSTGRES_DB=durpdeploy \
			-p 5432:5432 $(DEV_POSTGRES_IMAGE); \
	fi
	@printf '%s\n' \
		'Waiting for PostgreSQL...'
	@for i in $$(seq 1 60); do \
		if ! docker inspect -f '{{.State.Running}}' $(DEV_POSTGRES_CONTAINER) 2>/dev/null | grep -q true; then \
			echo 'PostgreSQL container stopped before becoming ready.' >&2; exit 1; \
		fi; \
		if docker exec $(DEV_POSTGRES_CONTAINER) pg_isready -U durpdeploy -d durpdeploy >/dev/null 2>&1; then \
			echo 'PostgreSQL is ready.'; exit 0; \
		fi; \
		sleep 1; \
	done; \
	echo 'Timed out waiting for PostgreSQL.' >&2; exit 1
	@echo 'Starting Durp Deploy with PostgreSQL...'
	DURPDEPLOY_DB='postgres://durpdeploy:durpdeploy@localhost:5432/durpdeploy?sslmode=disable' $(MAKE) dev

dev-mssql:
	@printf '%s\n' \
		'Database container: $(DEV_MSSQL_CONTAINER)' \
		'DURPDEPLOY_DB=sqlserver://sa:DurpDeploy%21Dev123@localhost:1433?database=master&encrypt=false&trustservercertificate=true'
	@if docker container inspect $(DEV_MSSQL_CONTAINER) >/dev/null 2>&1; then \
		if [ "$$(docker inspect -f '{{.State.Running}}' $(DEV_MSSQL_CONTAINER))" = true ]; then \
			echo 'Reusing running SQL Server container.'; \
		else \
			echo 'Starting existing SQL Server container.'; \
			docker start $(DEV_MSSQL_CONTAINER) >/dev/null; \
		fi; \
	else \
		docker run -d --name $(DEV_MSSQL_CONTAINER) \
			-e ACCEPT_EULA=Y \
			-e MSSQL_PID=Developer \
			-e MSSQL_SA_PASSWORD='DurpDeploy!Dev123' \
			-p 1433:1433 $(DEV_MSSQL_IMAGE); \
	fi
	@printf '%s\n' \
		'Waiting for SQL Server...'
	@for i in $$(seq 1 120); do \
		if ! docker inspect -f '{{.State.Running}}' $(DEV_MSSQL_CONTAINER) 2>/dev/null | grep -q true; then \
			echo 'SQL Server container stopped before becoming ready.' >&2; exit 1; \
		fi; \
		if docker exec $(DEV_MSSQL_CONTAINER) /opt/mssql-tools18/bin/sqlcmd -C -S localhost -U sa -P 'DurpDeploy!Dev123' -Q 'SELECT 1' >/dev/null 2>&1 || \
			docker exec $(DEV_MSSQL_CONTAINER) /opt/mssql-tools/bin/sqlcmd -S localhost -U sa -P 'DurpDeploy!Dev123' -Q 'SELECT 1' >/dev/null 2>&1; then \
			echo 'SQL Server is ready.'; exit 0; \
		fi; \
		sleep 1; \
	done; \
			echo 'Timed out waiting for SQL Server.' >&2; exit 1
	@echo 'Starting Durp Deploy with SQL Server...'
	DURPDEPLOY_DB='sqlserver://sa:DurpDeploy%21Dev123@localhost:1433?database=master&encrypt=false&trustservercertificate=true' $(MAKE) dev

# Manual E2E checks against the disposable database containers. The matching
# `dev-*` target must already be running in another terminal.
e2e-postgres:
	DURPDEPLOY_DB='postgres://durpdeploy:durpdeploy@localhost:5432/durpdeploy?sslmode=disable' \
	DURPDEPLOY_DB_CONTAINER='$(DEV_POSTGRES_CONTAINER)' ./scripts/e2e_db_test.sh postgres

e2e-mssql:
	DURPDEPLOY_DB='sqlserver://sa:DurpDeploy%21Dev123@localhost:1433?database=master&encrypt=false&trustservercertificate=true' \
	DURPDEPLOY_DB_CONTAINER='$(DEV_MSSQL_CONTAINER)' ./scripts/e2e_db_test.sh mssql

# Fails with a clear message instead of a cryptic "command not found" if
# openssl is missing and DURPDEPLOY_SECRET_KEY isn't already set.
check-openssl:
	@env_secret_key=$$(unset DURPDEPLOY_SECRET_KEY; \
		if [ -f "$(ENV_FILE)" ]; then . "$(ENV_FILE)"; fi; \
		printf '%s' "$${DURPDEPLOY_SECRET_KEY:-}"); \
	if [ -n "$$env_secret_key" ]; then DURPDEPLOY_SECRET_KEY="$$env_secret_key"; fi; \
	if [ -z "$$DURPDEPLOY_SECRET_KEY" ] && ! command -v openssl >/dev/null 2>&1; then \
		echo "ERROR: openssl not found. Install openssl, or set DURPDEPLOY_SECRET_KEY yourself." >&2; \
		exit 1; \
	fi

templ-generate:
	templ generate

swagger-spec:
	swagger generate spec -m -o internal/swagger/spec.json ./internal/handler/api

swagger-ui-copy: npm-install
	@mkdir -p static/swagger-ui
	cp node_modules/swagger-ui-dist/swagger-ui-bundle.js static/swagger-ui/
	cp node_modules/swagger-ui-dist/swagger-ui-standalone-preset.js static/swagger-ui/
	cp node_modules/swagger-ui-dist/swagger-ui.css static/swagger-ui/
	cp node_modules/swagger-ui-dist/favicon-32x32.png static/swagger-ui/
	cp node_modules/swagger-ui-dist/favicon-16x16.png static/swagger-ui/

npm-install:
	npm ci --ignore-scripts

tailwind-build: npm-install
	npx tailwindcss -i static/css/input.css -o static/css/tailwind.min.css --minify

js-build: npm-install
	npx esbuild static/js/app.js --bundle --minify --outfile=static/js/app.bundle.js

# Reformat Go source to 80-char width. Skips sqlc- and templ-generated files.
golines:
	golines --max-len=80 --ignore-generated -w .

# Dry-run: print a diff of what golines would change.
golines-check:
	@tmp=$$(mktemp); \
	if golines --max-len=80 --ignore-generated --dry-run . >"$$tmp" 2>&1; then \
		rm -f "$$tmp"; \
	else \
		cat "$$tmp"; \
		rm -f "$$tmp"; \
		exit 1; \
	fi

clean:
	rm -f $(BINARY_NAME)
	rm -f $(AGENT_BINARY_NAME)
	rm -f *_templ.go
	-podman rm -f $(DEV_POSTGRES_CONTAINER) $(DEV_MSSQL_CONTAINER)

# Go unit/integration tests (mirrors CI's exact command).
test: templ-generate
	go test -v -count=1 ./...

# Running-server E2E checks. Start `make dev` (or the matching database dev
# target) in another terminal first; this target never starts another server.
e2e-test:
	DURPDEPLOY_BASE_URL="$${DURPDEPLOY_BASE_URL:-http://localhost:8080}" \
	DURPDEPLOY_DB="$${DURPDEPLOY_DB:-durpdeploy.db}" ./scripts/e2e_db_test.sh sqlite

# Isolated E2E checks for CI or a clean-room local run. Unlike e2e-test, this
# target builds and starts its own temporary SQLite-backed server.
e2e-test-isolated: build check-openssl
	env_secret_key=$$(unset DURPDEPLOY_SECRET_KEY; \
		if [ -f "$(ENV_FILE)" ]; then . "$(ENV_FILE)"; fi; \
		printf '%s' "$${DURPDEPLOY_SECRET_KEY:-}"); \
	if [ -n "$$env_secret_key" ]; then DURPDEPLOY_SECRET_KEY="$$env_secret_key"; fi; \
	DURPDEPLOY_SECRET_KEY=$${DURPDEPLOY_SECRET_KEY:-$$(openssl rand -base64 32)} \
	DURPDEPLOY_ENV_FILE="$(ENV_FILE)" ./scripts/e2e_test.sh

# Complete deterministic MFA proof. The browser script creates its own isolated
# database/key and virtual authenticator after the shell contracts above.
mfa-e2e-test: e2e-test-isolated
	node scripts/mfa_browser_test.mjs

auth-mfa-e2e-go-prepare: check-openssl
	@command -v go >/dev/null 2>&1 || { echo "ERROR: go is required for auth/MFA E2E." >&2; exit 1; }
	@test -f static/swagger-ui/swagger-ui.css -a -f static/swagger-ui/swagger-ui-bundle.js || { echo "ERROR: generated Swagger UI is required; run make swagger-ui-copy first." >&2; exit 1; }
	templ generate

auth-mfa-e2e-browser-prepare: auth-mfa-e2e-go-prepare
	@command -v node >/dev/null 2>&1 || { echo "ERROR: node is required for auth/MFA browser E2E." >&2; exit 1; }
	@node -e 'require.resolve("playwright")' >/dev/null 2>&1 || { echo "ERROR: Playwright is required for auth/MFA browser E2E." >&2; exit 1; }

auth-mfa-e2e-sqlite-http: auth-mfa-e2e-go-prepare
	@printf '%s\n' '{"engine":"sqlite","suite":"auth-mfa-http","result":"start"}'
	DURPDEPLOY_AUTH_MFA_HTTP_MATRIX=1 ./scripts/e2e_test.sh
	DURPDEPLOY_AUTH_MFA_HTTP_MATRIX=1 DURPDEPLOY_AUTH_MFA_HTTP_MATRIX_SCRIPT=./scripts/auth_mfa_sqlite_authz_matrix.sh ./scripts/e2e_test.sh
	@printf '%s\n' '{"engine":"sqlite","suite":"auth-mfa-http","result":"pass"}'

auth-mfa-e2e-sqlite-browser: auth-mfa-e2e-browser-prepare
	@printf '%s\n' '{"engine":"sqlite","suite":"auth-mfa-browser","result":"start"}'
	node scripts/mfa_browser_test.mjs
	node scripts/auth_mfa_authz_browser_matrix.mjs
	@printf '%s\n' '{"engine":"sqlite","suite":"auth-mfa-browser","result":"pass"}'

auth-mfa-e2e-sqlite:
	+$(MAKE) --no-print-directory auth-mfa-e2e-sqlite-http
	+$(MAKE) --no-print-directory auth-mfa-e2e-sqlite-browser

auth-mfa-e2e-postgres: auth-mfa-e2e-go-prepare
	./scripts/auth_mfa_db_parity.sh postgres

auth-mfa-e2e-mssql: auth-mfa-e2e-go-prepare
	./scripts/auth_mfa_db_parity.sh mssql

auth-mfa-e2e:
	+$(MAKE) --no-print-directory auth-mfa-e2e-sqlite
	+$(MAKE) --no-print-directory auth-mfa-e2e-postgres
	+$(MAKE) --no-print-directory auth-mfa-e2e-mssql

# Runs the strict Playwright browser contract in the shared CI image. The source checkout
# and secret-free evidence directory stay on the host; Docker owns the browser.
MOBILE_BROWSER_IMAGE ?= durpdeploy-mobile-browser:local
MOBILE_BROWSER_RUN_ID ?= local-$$(date -u +%Y%m%dT%H%M%SZ)-$$$$

mobile-browser-container:
	mkdir -p artifacts/mobile
	docker build -f Dockerfile.mobile-browser -t $(MOBILE_BROWSER_IMAGE) .
	docker run --rm --init \
		--entrypoint /usr/local/bin/mobile-browser-container \
		-e MOBILE_RUN_ID="$(MOBILE_BROWSER_RUN_ID)" \
		-e MOBILE_ARTIFACT_DIR=/artifacts \
		-v "$(CURDIR):/workspace" \
		-v "$(CURDIR)/artifacts/mobile:/artifacts" \
		$(MOBILE_BROWSER_IMAGE)
