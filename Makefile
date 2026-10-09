.PHONY: ui build test demo
ui:
	cd web && bun install --frozen-lockfile && bun run build
build: ui
	CGO_ENABLED=0 go build -trimpath -o bin/tunwatch ./cmd/tunwatch
test: ui
	go test -race ./...
	go vet ./...
demo: build
	./bin/tunwatch -config config.demo.json
