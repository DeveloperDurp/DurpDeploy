.PHONY: build dev check-openssl templ-generate tailwind-build js-build npm-install golines golines-check clean test e2e-test swagger-spec

BINARY_NAME=durpdeploy
MAIN_PATH=cmd/server/main.go

build: swagger-spec swagger-ui-copy templ-generate tailwind-build js-build
	go build -o $(BINARY_NAME) $(MAIN_PATH)

# Hot-reload dev server. Watches .go/.templ/.sql in cmd, internal, views, migrations.
# Auto-generates a throwaway DURPDEPLOY_SECRET_KEY if one isn't already set in
# the environment, same as `make e2e-test` — the app refuses to start without one.
# ponytail: CSS/JS source changes need a separate `make tailwind-build && make js-build`
# and the air build to retrigger. Add a second air include_dir entry when that hurts.
dev: check-openssl
	DURPDEPLOY_SECRET_KEY=$${DURPDEPLOY_SECRET_KEY:-$$(openssl rand -base64 32)} go run github.com/air-verse/air@latest

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
	golines --max-len=80 --ignore-generated --dry-run .

clean:
	rm -f $(BINARY_NAME)
	rm -f *_templ.go

# Go unit/integration tests (mirrors CI's exact command).
test: templ-generate
	go test -v -count=1 ./...

# Bash end-to-end test: builds, runs the server, curls happy/cancel/validation
# paths. Auto-generates a throwaway DURPDEPLOY_SECRET_KEY if one isn't already
# set in the environment.
e2e-test: build check-openssl
	DURPDEPLOY_SECRET_KEY=$${DURPDEPLOY_SECRET_KEY:-$$(openssl rand -base64 32)} ./e2e_test.sh
