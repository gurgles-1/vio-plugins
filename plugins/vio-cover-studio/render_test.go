package main

import (
	"image"
	"image/color"
	"testing"
)

func testPoster(w, h int, c color.NRGBA) image.Image {
	img := image.NewNRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.SetNRGBA(x, y, c)
		}
	}
	return img
}

func TestLayoutCellsGrid(t *testing.T) {
	cells := layoutCells("grid", 1000, 1500)
	if len(cells) != 6 {
		t.Fatalf("grid should have 6 cells, got %d", len(cells))
	}
	// Cells must tile the frame exactly with no gaps or overlaps.
	seen := map[image.Point]bool{}
	for _, c := range cells {
		for y := c.Min.Y; y < c.Max.Y; y += 50 {
			for x := c.Min.X; x < c.Max.X; x += 50 {
				p := image.Point{x, y}
				if seen[p] {
					t.Fatalf("overlap at %v", p)
				}
				seen[p] = true
			}
		}
	}
	// 3x2: first row cells share y range, widths are equal thirds.
	if cells[0].Dx() != 1000/3 || cells[0].Dy() != 750 {
		t.Fatalf("bad grid cell size: %v", cells[0])
	}
	if cells[0].Min.Y != 0 || cells[3].Min.Y != 750 {
		t.Fatalf("bad grid rows: %v %v", cells[0], cells[3])
	}
}

func TestLayoutCellsSpotlight(t *testing.T) {
	cells := layoutCells("spotlight", 1920, 1080)
	if len(cells) != 5 {
		t.Fatalf("spotlight should have 5 cells, got %d", len(cells))
	}
	hero := cells[0]
	if hero.Dx() != 960 || hero.Dy() != 1080 {
		t.Fatalf("hero should be left half, got %v", hero)
	}
	// The 2x2 grid fills the right half.
	for i, c := range cells[1:] {
		if c.Min.X < 960 {
			t.Fatalf("cell %d leaks into hero half: %v", i+1, c)
		}
		if c.Dx() != 480 || c.Dy() != 540 {
			t.Fatalf("cell %d bad size: %v", i+1, c)
		}
	}
}

func TestLayoutCellsColumns(t *testing.T) {
	cells := layoutCells("columns", 1000, 1500)
	if len(cells) != 4 {
		t.Fatalf("columns should have 4 cells, got %d", len(cells))
	}
	for i, c := range cells {
		if c.Dx() != 250 || c.Dy() != 1500 {
			t.Fatalf("cell %d bad size: %v", i, c)
		}
		if c.Min.X != i*250 {
			t.Fatalf("cell %d bad x: %v", i, c)
		}
	}
}

func TestLayoutCellsUnknownFallsBackToGrid(t *testing.T) {
	if len(layoutCells("bogus", 1000, 1500)) != 6 {
		t.Fatal("unknown layout should fall back to grid")
	}
}

func TestBuildCoverTooFewPosters(t *testing.T) {
	posters := []image.Image{
		testPoster(200, 300, color.NRGBA{255, 0, 0, 255}),
		testPoster(200, 300, color.NRGBA{0, 255, 0, 255}),
		testPoster(200, 300, color.NRGBA{0, 0, 255, 255}),
	}
	if _, err := buildCover(posters, "Test", nil, "text", "grid", 1000, 1500); err == nil {
		t.Fatal("expected error for < 4 posters")
	}
}

func TestBuildCoverBadOverlay(t *testing.T) {
	posters := []image.Image{
		testPoster(200, 300, color.NRGBA{255, 0, 0, 255}),
		testPoster(200, 300, color.NRGBA{0, 255, 0, 255}),
		testPoster(200, 300, color.NRGBA{0, 0, 255, 255}),
		testPoster(200, 300, color.NRGBA{255, 255, 0, 255}),
	}
	if _, err := buildCover(posters, "Test", nil, "bogus", "grid", 1000, 1500); err == nil {
		t.Fatal("expected error for unknown overlay mode")
	}
	if _, err := buildCover(posters, "Test", nil, "logo", "grid", 1000, 1500); err == nil {
		t.Fatal("expected error for logo overlay without logo bytes")
	}
}

func TestBuildCoverSizes(t *testing.T) {
	cols := []color.NRGBA{
		{255, 0, 0, 255}, {0, 255, 0, 255}, {0, 0, 255, 255},
		{255, 255, 0, 255}, {255, 0, 255, 255}, {0, 255, 255, 255},
	}
	var posters []image.Image
	for _, c := range cols {
		posters = append(posters, testPoster(200, 300, c))
	}
	for _, layout := range []string{"grid", "spotlight", "columns"} {
		p, l, err := renderCover(posters, "Test Collection", nil, "text", layout)
		if err != nil {
			t.Fatalf("layout %s: %v", layout, err)
		}
		if len(p) == 0 || len(l) == 0 {
			t.Fatalf("layout %s: empty PNG output", layout)
		}
		// PNG magic.
		if p[0] != 0x89 || p[1] != 'P' || p[2] != 'N' || p[3] != 'G' {
			t.Fatalf("layout %s: portrait is not a PNG", layout)
		}
	}
}

func TestWrapTitle(t *testing.T) {
	face, err := loadFace(poppinsBold, 96)
	if err != nil {
		t.Fatal(err)
	}
	lines := wrapTitle(face, "The Lord of the Rings", 860)
	if len(lines) == 0 {
		t.Fatal("expected at least one line")
	}
	joined := ""
	for _, l := range lines {
		joined += l + " "
	}
	for _, w := range []string{"The", "Lord", "Rings"} {
		found := false
		for _, l := range lines {
			if containsWord(l, w) {
				found = true
			}
		}
		if !found {
			t.Fatalf("word %q lost in wrap: %v", w, lines)
		}
	}
	_ = joined
}

func containsWord(s, w string) bool {
	for _, f := range splitWords(s) {
		if f == w {
			return true
		}
	}
	return false
}

func splitWords(s string) []string {
	var out []string
	cur := ""
	for _, r := range s {
		if r == ' ' || r == '\t' {
			if cur != "" {
				out = append(out, cur)
				cur = ""
			}
			continue
		}
		cur += string(r)
	}
	if cur != "" {
		out = append(out, cur)
	}
	return out
}
