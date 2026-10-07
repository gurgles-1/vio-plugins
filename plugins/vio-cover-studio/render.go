package main

import (
	"bytes"
	_ "embed"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/disintegration/imaging"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed fonts/Poppins-Regular.ttf
var poppinsRegular []byte

//go:embed fonts/Poppins-Bold.ttf
var poppinsBold []byte

const (
	coverPortraitW  = 1000
	coverPortraitH  = 1500
	coverLandscapeW = 1920
	coverLandscapeH = 1080

	maxPosters     = 12
	minPosters     = 4
	posterFetchCap = 8 << 20
)

// layoutCells returns the mosaic cell rectangles for a layout, in prominence
// order (index 0 is the hero cell). Pure geometry — unit tested.
func layoutCells(layout string, W, H int) []image.Rectangle {
	switch layout {
	case "spotlight":
		// One large poster on the left half, 2x2 grid on the right half.
		cells := []image.Rectangle{
			image.Rect(0, 0, W/2, H),
		}
		rw, rh := W-W/2, H
		for r := 0; r < 2; r++ {
			for c := 0; c < 2; c++ {
				cells = append(cells, image.Rect(
					W/2+c*(rw/2), r*(rh/2),
					W/2+(c+1)*(rw/2), (r+1)*(rh/2),
				))
			}
		}
		return cells
	case "columns":
		// Four vertical strips.
		cells := make([]image.Rectangle, 0, 4)
		for c := 0; c < 4; c++ {
			cells = append(cells, image.Rect(c*W/4, 0, (c+1)*W/4, H))
		}
		return cells
	default: // "grid": 3x2 mosaic.
		cells := make([]image.Rectangle, 0, 6)
		for r := 0; r < 2; r++ {
			for c := 0; c < 3; c++ {
				cells = append(cells, image.Rect(
					c*W/3, r*H/2,
					(c+1)*W/3, (r+1)*H/2,
				))
			}
		}
		return cells
	}
}

// fetchPosterImage downloads and decodes one poster, capped in size.
func fetchPosterImage(client *http.Client, rawURL string) (image.Image, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" || strings.HasPrefix(rawURL, "data:") {
		return nil, fmt.Errorf("no usable poster url")
	}
	req, err := http.NewRequest(http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "vio-cover-studio/0.1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("poster download: status %d", resp.StatusCode)
	}
	img, _, err := image.Decode(io.LimitReader(resp.Body, posterFetchCap))
	if err != nil {
		return nil, fmt.Errorf("poster decode: %w", err)
	}
	return img, nil
}

// fetchPosterImages downloads up to max member posters, skipping failures.
func fetchPosterImages(titles []*vioTitle, max int) []image.Image {
	client := &http.Client{Timeout: 30 * time.Second}
	var out []image.Image
	for _, t := range titles {
		if len(out) >= max {
			break
		}
		if t == nil || t.PosterURL == "" {
			continue
		}
		img, err := fetchPosterImage(client, t.PosterURL)
		if err != nil {
			continue
		}
		out = append(out, img)
	}
	return out
}

// applyGradient darkens the top and bottom of the frame so overlaid
// text/logo stays readable.
func applyGradient(frame *image.NRGBA) {
	b := frame.Bounds()
	W, H := b.Dx(), b.Dy()
	topH := int(float64(H) * 0.28)
	botH := int(float64(H) * 0.38)
	for y := 0; y < H; y++ {
		var alpha uint8
		switch {
		case y < topH:
			t := 1 - float64(y)/float64(topH)
			alpha = uint8(200 * t * t)
		case y >= H-botH:
			t := float64(y-(H-botH)) / float64(botH)
			alpha = uint8(215 * t * t)
		}
		if alpha == 0 {
			continue
		}
		for x := 0; x < W; x++ {
			i := frame.PixOffset(x, y)
			inv := 255 - uint32(alpha)
			frame.Pix[i+0] = uint8((uint32(frame.Pix[i+0]) * inv) / 255)
			frame.Pix[i+1] = uint8((uint32(frame.Pix[i+1]) * inv) / 255)
			frame.Pix[i+2] = uint8((uint32(frame.Pix[i+2]) * inv) / 255)
		}
	}
}

type textFace struct {
	face font.Face
}

func loadFace(ttf []byte, size float64) (font.Face, error) {
	f, err := opentype.Parse(ttf)
	if err != nil {
		return nil, err
	}
	face, err := opentype.NewFace(f, &opentype.FaceOptions{
		Size:    size,
		DPI:     72,
		Hinting: font.HintingFull,
	})
	if err != nil {
		return nil, err
	}
	return face, nil
}

func measureText(face font.Face, s string) (w, h int) {
	d := font.Drawer{Face: face}
	w = d.MeasureString(s).Ceil()
	m := face.Metrics()
	h = (m.Ascent + m.Descent).Ceil()
	return w, h
}

// wrapTitle splits the title into lines that fit maxWidth.
func wrapTitle(face font.Face, title string, maxWidth int) []string {
	words := strings.Fields(title)
	if len(words) == 0 {
		return nil
	}
	var lines []string
	cur := ""
	for _, w := range words {
		try := strings.TrimSpace(cur + " " + w)
		tw, _ := measureText(face, try)
		if tw <= maxWidth || cur == "" {
			cur = try
			continue
		}
		lines = append(lines, cur)
		cur = w
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

// drawTitle renders the collection title centered in the lower third.
func drawTitle(frame *image.NRGBA, title string) error {
	title = strings.TrimSpace(title)
	if title == "" {
		return fmt.Errorf("empty title")
	}
	b := frame.Bounds()
	W, H := b.Dx(), b.Dy()
	maxWidth := int(float64(W) * 0.86)
	size := float64(W) / 9
	var face font.Face
	var lines []string
	for ; size >= 18; size -= 4 {
		f, err := loadFace(poppinsBold, size)
		if err != nil {
			return err
		}
		ls := wrapTitle(f, title, maxWidth)
		if len(ls) <= 3 {
			face, lines = f, ls
			break
		}
	}
	if face == nil {
		f, err := loadFace(poppinsBold, 18)
		if err != nil {
			return err
		}
		face, lines = f, wrapTitle(f, title, maxWidth)
	}
	m := face.Metrics()
	lineH := (m.Ascent + m.Descent).Ceil()
	totalH := lineH * len(lines)
	y := H - int(float64(H)*0.10) - totalH
	// Soft shadow for readability.
	for i, line := range lines {
		tw, _ := measureText(face, line)
		x := (W - tw) / 2
		ly := y + i*lineH
		for _, off := range [][2]int{{2, 2}, {0, 0}} {
			col := color.NRGBA{0, 0, 0, 200}
			if off[0] == 0 {
				col = color.NRGBA{255, 255, 255, 255}
			}
			d := font.Drawer{
				Dst:  frame,
				Src:  image.NewUniform(col),
				Face: face,
				Dot:  fixed.P(x+off[0], ly+off[1]+m.Ascent.Ceil()),
			}
			d.DrawString(line)
		}
	}
	return nil
}

// drawLogo composites the TMDB logo (transparent PNG) centered,
// scaled to fit within the frame.
func drawLogo(frame *image.NRGBA, logoPNG []byte) error {
	img, _, err := image.Decode(bytes.NewReader(logoPNG))
	if err != nil {
		return fmt.Errorf("logo decode: %w", err)
	}
	b := frame.Bounds()
	W, H := b.Dx(), b.Dy()
	maxW, maxH := int(float64(W)*0.72), int(float64(H)*0.30)
	iw, ih := img.Bounds().Dx(), img.Bounds().Dy()
	if iw == 0 || ih == 0 {
		return fmt.Errorf("logo has no size")
	}
	scale := math.Min(float64(maxW)/float64(iw), float64(maxH)/float64(ih))
	if scale > 1 {
		scale = 1 // never upscale a logo
	}
	nw, nh := int(float64(iw)*scale), int(float64(ih)*scale)
	if nw < 8 || nh < 8 {
		return fmt.Errorf("logo too small after scaling")
	}
	resized := imaging.Resize(img, nw, nh, imaging.Lanczos)
	x := (W - nw) / 2
	y := H - int(float64(H)*0.10) - nh
	draw.Draw(frame, image.Rect(x, y, x+nw, y+nh), resized, image.Point{}, draw.Over)
	return nil
}

// buildCover composites the mosaic for one frame size.
func buildCover(posters []image.Image, title string, logoPNG []byte, overlayMode, layout string, W, H int) (image.Image, error) {
	if len(posters) < minPosters {
		return nil, fmt.Errorf("need at least %d posters, have %d", minPosters, len(posters))
	}
	cells := layoutCells(layout, W, H)
	frame := imaging.New(W, H, color.NRGBA{10, 10, 14, 255})
	n := len(cells)
	if len(posters) < n {
		n = len(posters)
	}
	for i := 0; i < n; i++ {
		cell := cells[i]
		cw, ch := cell.Dx(), cell.Dy()
		filled := imaging.Fill(posters[i], cw, ch, imaging.Center, imaging.Lanczos)
		draw.Draw(frame, cell, filled, image.Point{}, draw.Src)
	}
	applyGradient(frame)
	switch overlayMode {
	case "logo":
		if len(logoPNG) == 0 {
			return nil, fmt.Errorf("logo overlay requested but no logo bytes")
		}
		if err := drawLogo(frame, logoPNG); err != nil {
			return nil, err
		}
	case "text":
		if err := drawTitle(frame, title); err != nil {
			return nil, err
		}
	case "none":
		// no overlay
	default:
		return nil, fmt.Errorf("unknown overlay mode %q", overlayMode)
	}
	return frame, nil
}

// renderCover builds portrait and landscape PNGs for a collection.
func renderCover(posters []image.Image, title string, logoPNG []byte, overlayMode, layout string) (portraitPNG, landscapePNG []byte, err error) {
	portrait, err := buildCover(posters, title, logoPNG, overlayMode, layout, coverPortraitW, coverPortraitH)
	if err != nil {
		return nil, nil, fmt.Errorf("portrait: %w", err)
	}
	landscape, err := buildCover(posters, title, logoPNG, overlayMode, layout, coverLandscapeW, coverLandscapeH)
	if err != nil {
		return nil, nil, fmt.Errorf("landscape: %w", err)
	}
	var pbuf, lbuf bytes.Buffer
	if err := png.Encode(&pbuf, portrait); err != nil {
		return nil, nil, err
	}
	if err := png.Encode(&lbuf, landscape); err != nil {
		return nil, nil, err
	}
	return pbuf.Bytes(), lbuf.Bytes(), nil
}
