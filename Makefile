.PHONY: build dev templ-generate tailwind-build js-build npm-install clean

BINARY_NAME=durpdeploy
MAIN_PATH=cmd/server/main.go

build: templ-generate tailwind-build js-build
	go build -o $(BINARY_NAME) $(MAIN_PATH)

dev:
	@echo "Install air or entr for hot-reload, or use:"
	@echo "  watch -n 1 'make build && ./$(BINARY_NAME)'"
	@echo "Or use templ generate --watch and go run $(MAIN_PATH)"

templ-generate:
	templ generate

npm-install:
	npm install

tailwind-build: npm-install
	npx tailwindcss -i static/css/input.css -o static/css/tailwind.min.css --minify

js-build: npm-install
	npx esbuild static/js/app.js --bundle --minify --outfile=static/js/app.bundle.js

clean:
	rm -f $(BINARY_NAME)
	rm -f *_templ.go
