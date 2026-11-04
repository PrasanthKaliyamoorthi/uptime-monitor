package db

import (
	"testing"
)

func TestAdminCreationAndReset(t *testing.T) {
	database, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.Close()

	repo := NewAdminRepo(database)

	has, err := repo.HasAdmin()
	if err != nil || has {
		t.Fatalf("expected HasAdmin = false initially")
	}

	const initialSecret = "AdminInitialSecret#2026!"
	const updatedSecret = "AdminUpdatedSecret#2026!"

	// Create admin
	admin, err := repo.CreateAdmin("superuser", initialSecret)
	if err != nil {
		t.Fatalf("failed to create admin: %v", err)
	}
	if admin.Username != "superuser" {
		t.Errorf("expected username superuser, got %s", admin.Username)
	}

	// Authenticate old password
	authAdmin, err := repo.Authenticate("superuser", initialSecret)
	if err != nil || authAdmin == nil {
		t.Fatalf("expected auth success with old password")
	}

	// Reset password via CLI helper
	err = repo.ResetPassword("superuser", updatedSecret)
	if err != nil {
		t.Fatalf("failed to reset password: %v", err)
	}

	// Old password must now fail
	_, err = repo.Authenticate("superuser", initialSecret)
	if err == nil {
		t.Fatalf("expected old password to fail after reset")
	}

	// New password must succeed
	authAdminNew, err := repo.Authenticate("superuser", updatedSecret)
	if err != nil || authAdminNew == nil {
		t.Fatalf("expected new password to authenticate successfully")
	}
}
