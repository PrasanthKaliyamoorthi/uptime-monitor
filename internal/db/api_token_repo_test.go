package db

import (
	"strings"
	"testing"
)

func TestAPITokenRepoLifecycle(t *testing.T) {
	database, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.Close()

	repo := NewAPITokenRepo(database)

	// 1. Create API token
	created, err := repo.Create("CI/CD Deployment Token")
	if err != nil {
		t.Fatalf("failed to create token: %v", err)
	}

	if created.ID == "" {
		t.Errorf("expected token ID to be populated")
	}
	if !strings.HasPrefix(created.Token, "upt_") {
		t.Errorf("expected token to have upt_ prefix, got %s", created.Token)
	}
	if !strings.HasPrefix(created.TokenPrefix, "upt_") {
		t.Errorf("expected token prefix to have upt_, got %s", created.TokenPrefix)
	}
	if created.TokenHash == "" {
		t.Errorf("expected token hash to be populated")
	}

	// 2. Lookup by Hash
	hash := HashToken(created.Token)
	token, err := repo.GetByHash(hash)
	if err != nil {
		t.Fatalf("failed to lookup token by hash: %v", err)
	}
	if token.ID != created.ID {
		t.Errorf("expected token ID %s, got %s", created.ID, token.ID)
	}
	if token.Name != "CI/CD Deployment Token" {
		t.Errorf("expected token name 'CI/CD Deployment Token', got %s", token.Name)
	}
	if token.LastUsedAt != nil {
		t.Errorf("expected last_used_at to be nil initially")
	}

	// 3. Touch last used
	if err := repo.TouchLastUsed(token.ID); err != nil {
		t.Fatalf("failed to touch last used: %v", err)
	}
	updated, _ := repo.GetByHash(hash)
	if updated.LastUsedAt == nil {
		t.Errorf("expected last_used_at to be updated")
	}

	// 4. List all tokens
	all, err := repo.GetAll()
	if err != nil {
		t.Fatalf("failed to list tokens: %v", err)
	}
	if len(all) != 1 {
		t.Fatalf("expected 1 token, got %d", len(all))
	}
	if all[0].ID != created.ID {
		t.Errorf("expected listed token to match created ID")
	}

	// 5. Delete token
	if err := repo.Delete(created.ID); err != nil {
		t.Fatalf("failed to delete token: %v", err)
	}
	afterDelete, _ := repo.GetAll()
	if len(afterDelete) != 0 {
		t.Errorf("expected 0 tokens after delete, got %d", len(afterDelete))
	}
}
