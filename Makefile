.PHONY: test test-integration test-e2e cover cover-all vuln up down logs fmt vet with-test-database

# Settings of the throw-away PostgreSQL container used by the database tests.
TEST_DB_CONTAINER := hospital-middleware-testdb
TEST_DB_PORT := 55432
# Published on the loopback address only, so the disposable database is not reachable from the network.
TEST_DB_URL := postgres://postgres:test@127.0.0.1:$(TEST_DB_PORT)/postgres?sslmode=disable

# Run every unit test.
test:
	go test ./...

# Run the tests that need a real PostgreSQL (build tag "integration") against a throw-away container.
test-integration: TEST_COMMAND = go test -count=1 -tags integration ./internal/repository/...
test-integration: with-test-database

# Run the end-to-end tests (build tag "e2e") through nginx. The stack must be running first: make up
# The base URL defaults to http://localhost:8080; set E2E_BASE_URL to test another deployment.
test-e2e:
	go test -count=1 -tags e2e ./tests/e2e/...

# Run the tests with coverage, write coverage.out and print the total.
# cmd/ only wires packages together, so coverage is measured on internal/.
cover:
	go test -coverprofile=coverage.out ./internal/...
	go tool cover -func=coverage.out | grep '^total:'

# Like cover, but the database tests run too, so the repository code is counted.
cover-all: TEST_COMMAND = go test -count=1 -tags integration -coverprofile=coverage.out ./internal/...
cover-all: with-test-database
	go tool cover -func=coverage.out | grep '^total:'

# Scan the dependencies and the standard library for known vulnerabilities (needs network access).
# The tool is run without being installed and without touching go.mod.
vuln:
	go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# Internal helper: runs $(TEST_COMMAND) with TEST_DATABASE_URL pointing at a fresh PostgreSQL container.
# The container is removed even when the tests fail, and make exits with the tests' own exit code.
with-test-database:
	-@docker rm -f $(TEST_DB_CONTAINER) >/dev/null 2>&1
	@docker run --rm -d --name $(TEST_DB_CONTAINER) -e POSTGRES_PASSWORD=test \
		-p 127.0.0.1:$(TEST_DB_PORT):5432 postgres:17-alpine >/dev/null
	@status=1; \
	for attempt in $$(seq 1 30); do \
		if docker exec $(TEST_DB_CONTAINER) pg_isready -h 127.0.0.1 -U postgres >/dev/null 2>&1; then status=0; break; fi; \
		sleep 1; \
	done; \
	if [ $$status -eq 0 ]; then \
		TEST_DATABASE_URL='$(TEST_DB_URL)' $(TEST_COMMAND); status=$$?; \
	else \
		echo "the test database did not become ready within 30 seconds"; \
	fi; \
	docker stop $(TEST_DB_CONTAINER) >/dev/null; \
	exit $$status

# Build the images and start the whole stack in the background (needs .env, see .env.example).
up:
	docker compose up --build -d

# Stop the stack; the database volume is kept (add -v to wipe it).
down:
	docker compose down

# Follow the logs of every service.
logs:
	docker compose logs -f --tail=100

# Format all Go files.
fmt:
	gofmt -l -w .

vet:
	go vet ./...
