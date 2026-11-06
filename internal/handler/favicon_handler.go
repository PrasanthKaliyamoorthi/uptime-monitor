package handler

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"math"
	"net/http"
	"net/url"
	"strings"
	"time"

	"uptime-monitor/internal/db"
)

// RoundCorners clips an image to a rounded rectangle with specified radius percentage (e.g. 0.08 for 8%).
func RoundCorners(src image.Image, radiusPercent float64) *image.RGBA {
	bounds := src.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	dst := image.NewRGBA(image.Rect(0, 0, w, h))

	minDim := float64(w)
	if float64(h) < minDim {
		minDim = float64(h)
	}
	r := minDim * radiusPercent
	if r < 1 {
		r = 1
	}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			fx, fy := float64(x), float64(y)
			var dist float64 = -1

			if fx < r && fy < r {
				// Top-left
				dx, dy := r-fx, r-fy
				dist = math.Sqrt(dx*dx + dy*dy)
			} else if fx > float64(w)-1-r && fy < r {
				// Top-right
				dx, dy := fx-(float64(w)-1-r), r-fy
				dist = math.Sqrt(dx*dx + dy*dy)
			} else if fx < r && fy > float64(h)-1-r {
				// Bottom-left
				dx, dy := r-fx, fy-(float64(h)-1-r)
				dist = math.Sqrt(dx*dx + dy*dy)
			} else if fx > float64(w)-1-r && fy > float64(h)-1-r {
				// Bottom-right
				dx, dy := fx-(float64(w)-1-r), fy-(float64(h)-1-r)
				dist = math.Sqrt(dx*dx + dy*dy)
			}

			srcCol := src.At(bounds.Min.X+x, bounds.Min.Y+y)
			if dist < 0 || dist <= r-0.5 {
				dst.Set(x, y, srcCol)
			} else if dist >= r+0.5 {
				dst.Set(x, y, color.Transparent)
			} else {
				// Smooth subpixel antialiasing
				coverage := r + 0.5 - dist
				rVal, gVal, bVal, aVal := srcCol.RGBA()
				newAlpha := uint8(float64(aVal>>8) * coverage)
				dst.Set(x, y, color.NRGBA{
					R: uint8(rVal >> 8),
					G: uint8(gVal >> 8),
					B: uint8(bVal >> 8),
					A: newAlpha,
				})
			}
		}
	}
	return dst
}

type FaviconHandler struct {
	statusPageRepo *db.StatusPageRepo
	httpClient     *http.Client
}

func NewFaviconHandler(spRepo *db.StatusPageRepo) *FaviconHandler {
	return &FaviconHandler{
		statusPageRepo: spRepo,
		httpClient: &http.Client{
			Timeout: 4 * time.Second,
		},
	}
}

const defaultRoundedSVG = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><rect width="100" height="100" rx="8" fill="#0f172a"/><circle cx="50" cy="50" r="30" fill="#10b981"/></svg>`

func (h *FaviconHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	rawTarget := strings.TrimSpace(r.URL.Query().Get("url"))

	if rawTarget == "" && h.statusPageRepo != nil {
		reqHost := strings.ToLower(ExtractRequestHost(r))
		pageSlug := strings.TrimSpace(r.URL.Query().Get("page"))

		if page, err := h.statusPageRepo.FindByDomain(reqHost); err == nil && page != nil && page.LogoURL != "" {
			rawTarget = page.LogoURL
		} else if pageSlug != "" {
			if page, err := h.statusPageRepo.GetBySlug(pageSlug); err == nil && page != nil && page.LogoURL != "" {
				rawTarget = page.LogoURL
			}
		} else if page, err := h.statusPageRepo.GetDefault(); err == nil && page != nil && page.LogoURL != "" {
			rawTarget = page.LogoURL
		}
	}

	if rawTarget == "" {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write([]byte(defaultRoundedSVG))
		return
	}

	// 1. Data URI handling
	if strings.HasPrefix(rawTarget, "data:") {
		parts := strings.SplitN(rawTarget, ",", 2)
		if len(parts) == 2 {
			meta := parts[0]
			dataStr := parts[1]

			if strings.Contains(meta, "image/svg+xml") {
				var svgBytes []byte
				if strings.Contains(meta, ";base64") {
					svgBytes, _ = base64.StdEncoding.DecodeString(dataStr)
				} else {
					decoded, _ := url.QueryUnescape(dataStr)
					svgBytes = []byte(decoded)
				}
				if len(svgBytes) > 0 {
					w.Header().Set("Content-Type", "image/svg+xml")
					w.Header().Set("Cache-Control", "public, max-age=86400")
					wrapped := wrapSVGRounded(string(svgBytes))
					_, _ = w.Write([]byte(wrapped))
					return
				}
			} else if strings.Contains(meta, ";base64") {
				imgBytes, err := base64.StdEncoding.DecodeString(dataStr)
				if err == nil {
					img, _, err := image.Decode(bytes.NewReader(imgBytes))
					if err == nil {
						rounded := RoundCorners(img, 0.08)
						w.Header().Set("Content-Type", "image/png")
						w.Header().Set("Cache-Control", "public, max-age=86400")
						_ = png.Encode(w, rounded)
						return
					}
				}
			}
		}
	}

	// 2. HTTP/HTTPS external image fetching
	if strings.HasPrefix(rawTarget, "http://") || strings.HasPrefix(rawTarget, "https://") {
		resp, err := h.httpClient.Get(rawTarget)
		if err == nil && resp.StatusCode == http.StatusOK {
			defer resp.Body.Close()
			cType := resp.Header.Get("Content-Type")

			if strings.Contains(cType, "image/svg+xml") {
				buf := new(bytes.Buffer)
				_, _ = buf.ReadFrom(resp.Body)
				w.Header().Set("Content-Type", "image/svg+xml")
				w.Header().Set("Cache-Control", "public, max-age=86400")
				_, _ = w.Write([]byte(wrapSVGRounded(buf.String())))
				return
			}

			img, _, err := image.Decode(resp.Body)
			if err == nil {
				rounded := RoundCorners(img, 0.08)
				w.Header().Set("Content-Type", "image/png")
				w.Header().Set("Cache-Control", "public, max-age=86400")
				_ = png.Encode(w, rounded)
				return
			}
		}
	}

	// Fallback to default rounded SVG
	w.Header().Set("Content-Type", "image/svg+xml")
	w.Header().Set("Cache-Control", "public, max-age=86400")
	_, _ = w.Write([]byte(defaultRoundedSVG))
}

func wrapSVGRounded(svgContent string) string {
	svgContent = strings.TrimSpace(svgContent)
	if strings.Contains(svgContent, "rx=") {
		return svgContent
	}
	return `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><defs><clipPath id="rc"><rect width="100" height="100" rx="8" ry="8"/></clipPath></defs><g clip-path="url(#rc)">` + svgContent + `</g></svg>`
}
