.PHONY: build dev templ-generate tailwind-build js-build npm-install golines golines-check clean test e2e-test

BINARY_NAME=durpdeploy
MAIN_PATH=cmd/server/main.go

build: templ-generate tailwind-build js-build
	go build -o $(BINARY_NAME) $(MAIN_PATH)

# Hot-reload dev server. Watches .go/.templ/.sql in cmd, internal, views, migrations.
# ponytail: CSS/JS source changes need a separate `make tailwind-build && make js-build`
# and the air build to retrigger. Add a second air include_dir entry when that hurts.
dev:
	go run github.com/air-verse/air@latest

templ-generate:
	templ generate

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

# Go unit/integration tests (mirrors CI's exact command).
test: templ-generate
	go test -v -count=1 ./...

# Bash end-to-end test: builds, runs the server, curls happy/cancel/validation
# paths. Auto-generates a throwaway DURPDEPLOY_SECRET_KEY if one isn't already
# set in the environment.
e2e-test: build
	DURPDEPLOY_SECRET_KEY=$${DURPDEPLOY_SECRET_KEY:-$$(openssl rand -base64 32)} ./e2e_test.sh
