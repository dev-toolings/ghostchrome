package media

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/jpeg" // for DecodeConfig support if baseline is jpg
	"image/png"
	"os"
)

// ImageDiffResult summarises a pixel-by-pixel comparison.
type ImageDiffResult struct {
	Width         int     `json:"width"`
	Height        int     `json:"height"`
	PixelsTotal   int     `json:"pixels_total"`
	PixelsChanged int     `json:"pixels_changed"`
	DiffRatio     float64 `json:"diff_ratio"`
	DiffPath      string  `json:"diff_path,omitempty"`
	Skipped       bool    `json:"skipped,omitempty"`
	SkipReason    string  `json:"skip_reason,omitempty"`
}

// DiffImages compares two images pixel by pixel. When dimensions differ, the
// result is flagged Skipped so the caller can decide (usually: fail or use the
// new image as the new baseline).
//
// If diffPath is non-empty, a PNG is written there highlighting every pixel
// where delta >= tolerance in red (original otherwise).
func DiffImages(baselinePNG, currentPNG []byte, tolerance int, diffPath string) (*ImageDiffResult, error) {
	baseline, _, err := image.Decode(bytes.NewReader(baselinePNG))
	if err != nil {
		return nil, fmt.Errorf("decode baseline: %w", err)
	}
	current, _, err := image.Decode(bytes.NewReader(currentPNG))
	if err != nil {
		return nil, fmt.Errorf("decode current: %w", err)
	}

	bb := baseline.Bounds()
	cb := current.Bounds()
	if bb.Dx() != cb.Dx() || bb.Dy() != cb.Dy() {
		return &ImageDiffResult{
			Width:      cb.Dx(),
			Height:     cb.Dy(),
			Skipped:    true,
			SkipReason: fmt.Sprintf("dimensions differ: baseline=%dx%d current=%dx%d", bb.Dx(), bb.Dy(), cb.Dx(), cb.Dy()),
		}, nil
	}

	w := cb.Dx()
	h := cb.Dy()
	changed := 0
	var diffImg *image.RGBA
	if diffPath != "" {
		diffImg = image.NewRGBA(image.Rect(0, 0, w, h))
	}

	red := color.RGBA{R: 255, G: 0, B: 0, A: 255}

	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			br, bg, bb8, _ := colorComponents(baseline.At(bb.Min.X+x, bb.Min.Y+y))
			cr, cg, cb8, _ := colorComponents(current.At(cb.Min.X+x, cb.Min.Y+y))

			delta := absDiff(br, cr) + absDiff(bg, cg) + absDiff(bb8, cb8)
			if delta > tolerance*3 {
				changed++
				if diffImg != nil {
					diffImg.Set(x, y, red)
				}
			} else if diffImg != nil {
				diffImg.Set(x, y, current.At(cb.Min.X+x, cb.Min.Y+y))
			}
		}
	}

	total := w * h
	result := &ImageDiffResult{
		Width:         w,
		Height:        h,
		PixelsTotal:   total,
		PixelsChanged: changed,
		DiffRatio:     float64(changed) / float64(total),
	}

	if diffImg != nil && diffPath != "" {
		f, err := os.OpenFile(diffPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
		if err != nil {
			return result, fmt.Errorf("write diff: %w", err)
		}
		defer f.Close()
		if err := png.Encode(f, diffImg); err != nil {
			return result, fmt.Errorf("encode diff: %w", err)
		}
		result.DiffPath = diffPath
	}

	return result, nil
}

func colorComponents(c color.Color) (int, int, int, int) {
	r, g, b, a := c.RGBA()
	return int(r >> 8), int(g >> 8), int(b >> 8), int(a >> 8)
}

func absDiff(a, b int) int {
	if a > b {
		return a - b
	}
	return b - a
}
