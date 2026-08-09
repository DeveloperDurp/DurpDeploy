.PHONY: build dev dev-postgres dev-mssql e2e-postgres e2e-mssql check-openssl templ-generate tailwind-build js-build npm-install golines golines-check clean test e2e-test swagger-spec

BINARY_NAME=durpdeploy
MAIN_PATH=cmd/server/main.go
DEV_POSTGRES_CONTAINER ?= durpdeploy-dev-postgres
DEV_POSTGRES_IMAGE ?= postgres:16-alpine
DEV_MSSQL_CONTAINER ?= durpdeploy-dev-mssql
DEV_MSSQL_IMAGE ?= mcr.microsoft.com/mssql/server:2022-latest

build: swagger-spec swagger-ui-copy templ-generate tailwind-build js-build
	go build -o $(BINARY_NAME) $(MAIN_PATH)

# Hot-reload dev server. Watches .go/.templ/.sql in cmd, internal, views, migrations.
# Auto-generates a throwaway DURPDEPLOY_SECRET_KEY if one isn't already set in
# the environment, same as `make e2e-test` — the app refuses to start without one.
# ponytail: CSS/JS source changes need a separate `make tailwind-build && make js-build`
# and the air build to retrigger. Add a second air include_dir entry when that hurts.
dev: check-openssl
	DURPDEPLOY_SECRET_KEY=$${DURPDEPLOY_SECRET_KEY:-$$(openssl rand -base64 32)} go run github.com/air-verse/air@latest

# Disposable database containers for manual backend testing. Stop them with
# `docker stop $(DEV_POSTGRES_CONTAINER)` or `docker stop $(DEV_MSSQL_CONTAINER)`.
dev-postgres:
	docker pull $(DEV_POSTGRES_IMAGE)
	-docker rm -f $(DEV_POSTGRES_CONTAINER)
	@printf '%s\n' \
		'Database container: $(DEV_POSTGRES_CONTAINER)' \
		'DURPDEPLOY_DB=postgres://durpdeploy:durpdeploy@localhost:5432/durpdeploy?sslmode=disable'
	docker run -d --name $(DEV_POSTGRES_CONTAINER) \
		-e POSTGRES_USER=durpdeploy \
		-e POSTGRES_PASSWORD=durpdeploy \
		-e POSTGRES_DB=durpdeploy \
		-p 5432:5432 $(DEV_POSTGRES_IMAGE)
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
	docker pull $(DEV_MSSQL_IMAGE)
	-docker rm -f $(DEV_MSSQL_CONTAINER)
	@printf '%s\n' \
		'Database container: $(DEV_MSSQL_CONTAINER)' \
		'DURPDEPLOY_DB=sqlserver://sa:DurpDeploy%21Dev123@localhost:1433?database=master&encrypt=false&trustservercertificate=true'
	docker run -d --name $(DEV_MSSQL_CONTAINER) \
		-e ACCEPT_EULA=Y \
		-e MSSQL_PID=Developer \
		-e MSSQL_SA_PASSWORD='DurpDeploy!Dev123' \
		-p 1433:1433 $(DEV_MSSQL_IMAGE)
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
	@if [ -z "$$DURPDEPLOY_SECRET_KEY" ] && ! command -v openssl >/dev/null 2>&1; then \
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
	npm install

tailwind-build: npm-install
	npx tailwindcss -i static/css/input.css -o static/css/tailwind.min.css --minify

js-build: npm-install
	npx esbuild static/js/app.js --bundle --minify --outfile=static/js/app.bundle.js

# Reformat Go source to 80-char width. Skips sqlc- and templ-generated files.
golines:
	golines --max-len=80 --ignore-generated -w .

# Dry-run: print a diff of what golines would change.
golines-check:
	golines --max-len=80 --ignore-generated --dry-run .

clean:
	rm -f $(BINARY_NAME)
	rm -f *_templ.go
	-podman rm -f $(DEV_POSTGRES_CONTAINER) $(DEV_MSSQL_CONTAINER)

# Go unit/integration tests (mirrors CI's exact command).
test: templ-generate
	go test -v -count=1 ./...

# Bash end-to-end test: builds, runs the server, curls happy/cancel/validation
# paths. Auto-generates a throwaway DURPDEPLOY_SECRET_KEY if one isn't already
# set in the environment.
e2e-test: build check-openssl
	DURPDEPLOY_SECRET_KEY=$${DURPDEPLOY_SECRET_KEY:-$$(openssl rand -base64 32)} ./scripts/e2e_test.sh
