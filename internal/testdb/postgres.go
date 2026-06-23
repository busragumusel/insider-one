package testdb

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/docker/go-connections/nat"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

func URL(t testing.TB) string {
	t.Helper()

	if databaseURL := os.Getenv("TEST_DATABASE_URL"); databaseURL != "" {
		return isolatedSchemaURL(t, databaseURL)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	t.Cleanup(cancel)

	req := testcontainers.ContainerRequest{
		Image:        "postgres:16-alpine",
		ExposedPorts: []string{"5432/tcp"},
		Env: map[string]string{
			"POSTGRES_DB":       "insider_one",
			"POSTGRES_USER":     "insider",
			"POSTGRES_PASSWORD": "insider",
		},
		WaitingFor: wait.ForListeningPort("5432/tcp"),
	}

	container, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
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

	host, err := container.Host(ctx)
	if err != nil {
		t.Fatalf("get Postgres test container host: %v", err)
	}

	port, err := container.MappedPort(ctx, nat.Port("5432/tcp"))
	if err != nil {
		t.Fatalf("get Postgres test container port: %v", err)
	}

	return fmt.Sprintf(
		"postgres://insider:insider@%s:%s/insider_one?sslmode=disable",
		host,
		port.Port(),
	)
}

func isDockerUnavailable(err error) bool {
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "cannot connect to the docker daemon") ||
		strings.Contains(message, "docker daemon") ||
		strings.Contains(message, "colima") ||
		strings.Contains(message, "permission denied")
}

func isolatedSchemaURL(t testing.TB, databaseURL string) string {
	t.Helper()

	schema := "test_" + randomHex(t)
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		t.Fatalf("open Postgres test database: %v", err)
	}
	t.Cleanup(func() {
		_ = db.Close()
	})

	if _, err := db.Exec(`CREATE SCHEMA ` + schema); err != nil {
		t.Fatalf("create Postgres test schema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = db.Exec(`DROP SCHEMA IF EXISTS ` + schema + ` CASCADE`)
	})

	separator := "?"
	if strings.Contains(databaseURL, "?") {
		separator = "&"
	}

	return fmt.Sprintf("%s%soptions=-csearch_path%%3D%s", databaseURL, separator, schema)
}

func randomHex(t testing.TB) string {
	t.Helper()

	var bytes [8]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		t.Fatalf("generate Postgres test schema suffix: %v", err)
	}

	return hex.EncodeToString(bytes[:])
}
