package image

import (
	"image/png"
	"os"
	"path/filepath"
	"testing"
)

func TestConvertSVGToPNGPreservesDimensions(t *testing.T) {
	dir := t.TempDir()
	svgPath := filepath.Join(dir, "input.svg")
	pngPath := filepath.Join(dir, "output.png")
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 10 15"><rect width="10" height="15" fill="red"/></svg>`
	if err := os.WriteFile(svgPath, []byte(svg), 0600); err != nil {
		t.Fatal(err)
	}
	if err := ConvertSVGToPNG(svgPath, pngPath); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(pngPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	img, err := png.Decode(file)
	if err != nil {
		t.Fatal(err)
	}
	if size := img.Bounds().Size(); size.X != 10 || size.Y != 15 {
		t.Fatalf("PNG size = %v, want 10x15", size)
	}
	if r, g, b, a := img.At(5, 8).RGBA(); r != 0xffff || g != 0 || b != 0 || a != 0xffff {
		t.Fatalf("rectangle color = (%d, %d, %d, %d)", r, g, b, a)
	}
}
