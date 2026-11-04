package db

import (
	"testing"
	"uptime-monitor/internal/model"
)

func TestStatusPageRepoCreation(t *testing.T) {
	database, err := InitDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.Close()

	repo := NewStatusPageRepo(database)

	// 1. Create status page 1 with empty domain
	p1 := &model.StatusPage{
		Slug: "page1",
		Name: "Page One",
		Domain: "",
	}
	if err := repo.Create(p1); err != nil {
		t.Fatalf("failed to create page 1: %v", err)
	}

	// 2. Create status page 2 with empty domain
	p2 := &model.StatusPage{
		Slug: "page2",
		Name: "Page Two",
		Domain: "",
	}
	if err := repo.Create(p2); err != nil {
		t.Fatalf("failed to create page 2 with empty domain: %v", err)
	}

	// 3. Create status page 3 with whitespace domain
	p3 := &model.StatusPage{
		Slug: "page3",
		Name: "Page Three",
		Domain: "   ",
	}
	if err := repo.Create(p3); err != nil {
		t.Fatalf("failed to create page 3 with whitespace domain: %v", err)
	}

	// 4. Create status page 4 with domain
	p4 := &model.StatusPage{
		Slug: "page4",
		Name: "Page Four",
		Domain: "status.domain.com",
	}
	if err := repo.Create(p4); err != nil {
		t.Fatalf("failed to create page 4: %v", err)
	}

	// 5. Duplicate domain should fail
	p5 := &model.StatusPage{
		Slug: "page5",
		Name: "Page Five",
		Domain: "status.domain.com",
	}
	if err := repo.Create(p5); err == nil {
		t.Fatalf("expected error on duplicate domain, got nil")
	}
}
