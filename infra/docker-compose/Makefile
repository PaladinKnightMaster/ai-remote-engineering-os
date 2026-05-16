.PHONY: up down logs test offline-check

up:
	docker compose -f docker-compose.mvp.yml up -d

down:
	docker compose -f docker-compose.mvp.yml down

logs:
	docker compose -f docker-compose.mvp.yml logs -f

test:
	@echo "Run unit/integration/API tests here"

offline-check:
	@echo "Verify all images exist in local registry and install works without internet"
