package testdb

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
)

func URL(t testing.TB) string {
	t.Helper()
	if databaseURL := os.Getenv("TEST_DATABASE_URL"); databaseURL != "" {
		return databaseURL
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	container, err := tcpostgres.RunContainer(ctx,
		tcpostgres.WithDatabase("insider_one"),
		tcpostgres.WithUsername("insider"),
		tcpostgres.WithPassword("insider"),
	)
	if err != nil {
		if isDockerUnavailable(err) {
			t.Skipf("Docker is required for Testcontainers Postgres: %v", err)
		}
		t.Fatalf("start Postgres test container: %v", err)
	}
	t.Cleanup(func() {
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer shutdownCancel()
		_ = container.Terminate(shutdownCtx)
	})

	databaseURL, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("build Postgres test connection string: %v", err)
	}
	return databaseURL
}

func isDockerUnavailable(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "cannot connect to the docker daemon") ||
		strings.Contains(message, "docker daemon") ||
		strings.Contains(message, "colima") ||
		strings.Contains(message, "permission denied")
}
