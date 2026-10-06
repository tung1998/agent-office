.PHONY: build test start dev-api dev-ui ui-install ui-build ui-start up

build:
	go build -o bin/office ./cmd/office

test:
	go vet ./... && go test ./...
	cd dashboard && node scripts/check-i18n.mjs && pnpm test:unit

# office run: supervisor with API + dashboard, restarts and self-updates
start: build ui-build
	./bin/office run

dev-api: build
	./bin/office serve

ui-install:
	cd dashboard && pnpm install

dev-ui:
	cd dashboard && pnpm dev

ui-build:
	cd dashboard && node scripts/check-i18n.mjs && pnpm typecheck && pnpm build

# dashboard port: 2704 (dev and built)
ui-start:
	cd dashboard && PORT=2704 node .output/server/index.mjs

up:
	docker compose up -d --build
