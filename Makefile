.PHONY: build test frontend release-snapshot

frontend:
	npm ci
	npm run build

build: frontend
	mkdir -p dist
	go build -o dist/pr-inbox ./cmd/pr-inbox

test: frontend
	./node_modules/.bin/tsc --noEmit
	go test ./...

release-snapshot: frontend
	goreleaser release --snapshot --clean
