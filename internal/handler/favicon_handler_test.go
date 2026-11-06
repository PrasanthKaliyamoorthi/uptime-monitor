package handler

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"testing"

	"uptime-monitor/internal/db"
	"uptime-monitor/internal/model"
)

func TestRoundCorners(t *testing.T) {
	// Create a 100x100 solid red square image
	src := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			src.Set(x, y, color.RGBA{R: 255, G: 0, B: 0, A: 255})
		}
	}

	// Apply 8% radius (8px radius)
	rounded := RoundCorners(src, 0.08)

	// Center should remain solid red
	centerCol := rounded.At(50, 50).(color.RGBA)
	if centerCol.R != 255 || centerCol.A != 255 {
		t.Fatalf("expected center pixel to be opaque red, got: %+v", centerCol)
	}

	// Extreme top-left corner (0, 0) should be transparent
	topLeftCol := rounded.At(0, 0)
	_, _, _, a := topLeftCol.RGBA()
	if a != 0 {
		t.Fatalf("expected top-left corner to be transparent, got alpha: %d", a)
	}

	// Extreme bottom-right corner (99, 99) should be transparent
	bottomRightCol := rounded.At(99, 99)
	_, _, _, a2 := bottomRightCol.RGBA()
	if a2 != 0 {
		t.Fatalf("expected bottom-right corner to be transparent, got alpha: %d", a2)
	}
}

func TestFaviconHandler(t *testing.T) {
	database, err := db.InitDB(":memory:")
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer database.Close()

	spRepo := db.NewStatusPageRepo(database)

	// Create test status page with a base64 png logo
	src := image.NewRGBA(image.Rect(0, 0, 50, 50))
	for y := 0; y < 50; y++ {
		for x := 0; x < 50; x++ {
			src.Set(x, y, color.RGBA{R: 0, G: 128, B: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, src)
	b64 := "data:image/png;base64," + base64.StdEncoding.EncodeToString(buf.Bytes())

	_ = spRepo.Create(&model.StatusPage{
		Name:      "Test Status",
		Slug:      "test",
		IsDefault: true,
		LogoURL:   b64,
	})

	handler := NewFaviconHandler(spRepo)

	// 1. Request default /favicon.png
	req := httptest.NewRequest("GET", "/favicon.png", nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("expected image/png content type, got: %s", rec.Header().Get("Content-Type"))
	}

	// 2. Decode returned image and verify corners are rounded
	img, err := png.Decode(rec.Body)
	if err != nil {
		t.Fatalf("failed to decode response png: %v", err)
	}
	_, _, _, cornerAlpha := img.At(0, 0).RGBA()
	if cornerAlpha != 0 {
		t.Errorf("expected corner to be transparent, got alpha: %d", cornerAlpha)
	}
}
