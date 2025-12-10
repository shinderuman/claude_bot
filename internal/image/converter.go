package image

import (
	"fmt"
	"image"
	"image/png"
	"os"

	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
)

// ConvertSVGToPNG converts an SVG file to a PNG file
func ConvertSVGToPNG(svgPath, pngPath string) error {
	// Read SVG file
	in, err := os.Open(svgPath)
	if err != nil {
		return fmt.Errorf("failed to open SVG file: %w", err)
	}
	defer in.Close() //nolint:errcheck

	// Parse SVG
	icon, err := oksvg.ReadIconStream(in)
	if err != nil {
		return fmt.Errorf("failed to parse SVG: %w", err)
	}

	// Set target size (use original size or default to something reasonable if not set)
	w, h := int(icon.ViewBox.W), int(icon.ViewBox.H)
	if w == 0 || h == 0 {
		w, h = 512, 512 // Default size if not specified
	}
	icon.SetTarget(0, 0, float64(w), float64(h))

	// Create image
	rgba := image.NewRGBA(image.Rect(0, 0, w, h))

	// Create scanner/rasterizer
	scanner := rasterx.NewScannerGV(w, h, rgba, rgba.Bounds())
	raster := rasterx.NewDasher(w, h, scanner)

	// Draw
	icon.Draw(raster, 1.0)

	// Save PNG
	out, err := os.Create(pngPath)
	if err != nil {
		return fmt.Errorf("failed to create PNG file: %w", err)
	}
	defer out.Close() //nolint:errcheck

	if err := png.Encode(out, rgba); err != nil {
		return fmt.Errorf("failed to encode PNG: %w", err)
	}

	return nil
}
