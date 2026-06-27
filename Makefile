.PHONY: build dev templ-generate tailwind-build clean

BINARY_NAME=durpdeploy
MAIN_PATH=cmd/server/main.go

build: templ-generate tailwind-build
	go build -o $(BINARY_NAME) $(MAIN_PATH)

dev:
	@echo "Install air or entr for hot-reload, or use:"
	@echo "  watch -n 1 'make build && ./$(BINARY_NAME)'"
	@echo "Or use templ generate --watch and go run $(MAIN_PATH)"

templ-generate:
	templ generate

tailwind-build:
	npx tailwindcss -i static/css/input.css -o static/css/tailwind.min.css --minify

clean:
	rm -f $(BINARY_NAME)
	rm -f *_templ.go
