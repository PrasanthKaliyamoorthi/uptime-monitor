package db

import (
	"fmt"
	"os"
	"testing"
	"time"
)

func TestPersistentSessionAcrossInstances(t *testing.T) {
	dbFile := fmt.Sprintf("./test_session_%d.db", os.Getpid())
	defer os.Remove(dbFile)

	database, err := InitDB(dbFile)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.Close()

	sessionRepo := NewSessionRepo(database)

	token := "sample-session-token-12345"
	adminID := "admin-1"
	username := "admin"
	expiresAt := time.Now().UTC().Add(1 * time.Hour)

	// Save session
	if err := sessionRepo.Create(token, adminID, username, expiresAt); err != nil {
		t.Fatalf("failed to create session: %v", err)
	}

	// Read session using a new repo instance simulating server restart
	newRepo := NewSessionRepo(database)
	sess, err := newRepo.Get(token)
	if err != nil || sess == nil {
		t.Fatalf("expected session to persist across instances, got: %v", err)
	}

	if sess.Username != username || sess.AdminID != adminID {
		t.Errorf("expected session for %s, got: %v", username, sess)
	}

	// Test delete
	if err := newRepo.Delete(token); err != nil {
		t.Fatalf("failed to delete session: %v", err)
	}

	sessDeleted, _ := newRepo.Get(token)
	if sessDeleted != nil {
		t.Errorf("expected session to be deleted")
	}
}
