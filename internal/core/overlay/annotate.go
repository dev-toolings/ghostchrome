package overlay

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	_ "image/jpeg"
	"image/png"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/dev-toolings/ghostchrome/engine"
	"github.com/go-rod/rod"
	"github.com/go-rod/rod/lib/proto"
)

// AddExtractionBoxes enriches interactive nodes with viewport-relative CSS
// pixel rectangles. Missing or detached nodes are skipped because pages may
// mutate while their boxes are being collected.
func AddExtractionBoxes(page *rod.Page, result *engine.ExtractionResult) {
	if page == nil || result == nil || len(result.Refs) == 0 {
		return
	}
	boxes := make(map[string]engine.ElementBox, len(result.Refs))
	for ref, node := range result.Refs {
		if node.BackendNodeID == 0 {
			continue
		}
		model, err := proto.DOMGetBoxModel{BackendNodeID: node.BackendNodeID}.Call(page)
		if err != nil || model == nil || model.Model == nil || len(model.Model.Border) < 8 {
			continue
		}
		border := model.Model.Border
		minX := minF(border[0], border[2], border[4], border[6])
		minY := minF(border[1], border[3], border[5], border[7])
		maxX := maxF(border[0], border[2], border[4], border[6])
		maxY := maxF(border[1], border[3], border[5], border[7])
		box := engine.ElementBox{
			X:      int(math.Round(minX)),
			Y:      int(math.Round(minY)),
			Width:  int(math.Round(maxX - minX)),
			Height: int(math.Round(maxY - minY)),
		}
		if box.Width <= 0 || box.Height <= 0 {
			continue
		}
		boxes[ref] = box
		node.Box = &box
		result.Refs[ref] = node
	}
	applyBoxesToNodes(result.Nodes, boxes)
}

func applyBoxesToNodes(nodes []engine.ExtractedNode, boxes map[string]engine.ElementBox) {
	for i := range nodes {
		if box, ok := boxes[nodes[i].Ref]; ok {
			copy := box
			nodes[i].Box = &copy
		}
		applyBoxesToNodes(nodes[i].Children, boxes)
	}
}

type annotationBox struct {
	num  int
	x, y int
	w, h int
}

var annotationPalette = [...]color.NRGBA{
	{255, 50, 50, 255},
	{50, 120, 255, 255},
	{50, 200, 50, 255},
	{255, 160, 0, 255},
	{180, 50, 220, 255},
	{0, 200, 200, 255},
	{255, 100, 150, 255},
	{140, 180, 0, 255},
}

// AnnotateScreenshot overlays numbered borders on each interactive element
// identified by the snapshot's refs. Input must be PNG; output is always PNG.
func AnnotateScreenshot(page *rod.Page, snapshot *engine.PageSnapshot, screenshotPNG []byte) ([]byte, error) {
	if snapshot == nil || len(snapshot.Refs) == 0 {
		return screenshotPNG, nil
	}

	img, _, err := image.Decode(bytes.NewReader(screenshotPNG))
	if err != nil {
		return nil, fmt.Errorf("decode screenshot: %w", err)
	}

	bounds := img.Bounds()
	dst := image.NewNRGBA(bounds)
	draw.Draw(dst, bounds, img, bounds.Min, draw.Src)

	dpr := devicePixelRatio(page)
	boxes := collectRefBoxes(page, snapshot, dpr, bounds)

	for i, box := range boxes {
		c := annotationPalette[i%len(annotationPalette)]
		drawBorder(dst, box.x, box.y, box.w, box.h, c, 2)
		labelY := box.y - 12
		if labelY < 0 {
			labelY = box.y + 2
		}
		drawRefLabel(dst, box.x, labelY, box.num, c)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, fmt.Errorf("encode annotated: %w", err)
	}
	return buf.Bytes(), nil
}

func devicePixelRatio(page *rod.Page) float64 {
	res, err := page.Eval(`() => window.devicePixelRatio`)
	if err != nil {
		return 1.0
	}
	v := res.Value.Num()
	if v <= 0 {
		return 1.0
	}
	return v
}

func collectRefBoxes(page *rod.Page, snapshot *engine.PageSnapshot, dpr float64, imgBounds image.Rectangle) []annotationBox {
	var boxes []annotationBox
	for refStr, refInfo := range snapshot.Refs {
		if refInfo.BackendNodeID == 0 {
			continue
		}
		num, err := strconv.Atoi(strings.TrimPrefix(refStr, "@"))
		if err != nil {
			continue
		}
		model, err := proto.DOMGetBoxModel{BackendNodeID: refInfo.BackendNodeID}.Call(page)
		if err != nil || model == nil || model.Model == nil {
			continue
		}
		border := model.Model.Border
		if len(border) < 8 {
			continue
		}
		minX := minF(border[0], border[2], border[4], border[6])
		minY := minF(border[1], border[3], border[5], border[7])
		maxX := maxF(border[0], border[2], border[4], border[6])
		maxY := maxF(border[1], border[3], border[5], border[7])

		x := int(minX * dpr)
		y := int(minY * dpr)
		w := int((maxX - minX) * dpr)
		h := int((maxY - minY) * dpr)

		if x < 0 {
			w += x
			x = 0
		}
		if y < 0 {
			h += y
			y = 0
		}
		if x+w > imgBounds.Max.X {
			w = imgBounds.Max.X - x
		}
		if y+h > imgBounds.Max.Y {
			h = imgBounds.Max.Y - y
		}
		if w <= 0 || h <= 0 {
			continue
		}
		boxes = append(boxes, annotationBox{num: num, x: x, y: y, w: w, h: h})
	}
	sort.Slice(boxes, func(i, j int) bool { return boxes[i].num < boxes[j].num })
	return boxes
}

func drawBorder(img *image.NRGBA, x, y, w, h int, c color.NRGBA, thickness int) {
	bx := img.Bounds()
	for t := 0; t < thickness; t++ {
		for px := x; px < x+w; px++ {
			setClipped(img, bx, px, y+t, c)
			setClipped(img, bx, px, y+h-1-t, c)
		}
		for py := y; py < y+h; py++ {
			setClipped(img, bx, x+t, py, c)
			setClipped(img, bx, x+w-1-t, py, c)
		}
	}
}

func drawRefLabel(img *image.NRGBA, x, y, num int, bg color.NRGBA) {
	text := strconv.Itoa(num)
	const charW, charH, pad = 5, 7, 2
	labelW := len(text)*(charW+1) - 1 + pad*2
	labelH := charH + pad*2
	bx := img.Bounds()

	for py := y; py < y+labelH; py++ {
		for px := x; px < x+labelW; px++ {
			setClipped(img, bx, px, py, bg)
		}
	}

	white := color.NRGBA{255, 255, 255, 255}
	cx := x + pad
	for _, ch := range text {
		d := int(ch - '0')
		if d >= 0 && d <= 9 {
			drawGlyph(img, bx, cx, y+pad, d, white)
		}
		cx += charW + 1
	}
}

// 5×7 bitmap font for digits 0-9. Each row is a 5-bit mask (MSB = leftmost).
var digitFont = [10][7]uint8{
	{0x0E, 0x11, 0x13, 0x15, 0x19, 0x11, 0x0E}, // 0
	{0x04, 0x0C, 0x04, 0x04, 0x04, 0x04, 0x0E}, // 1
	{0x0E, 0x11, 0x01, 0x06, 0x08, 0x10, 0x1F}, // 2
	{0x0E, 0x11, 0x01, 0x06, 0x01, 0x11, 0x0E}, // 3
	{0x02, 0x06, 0x0A, 0x12, 0x1F, 0x02, 0x02}, // 4
	{0x1F, 0x10, 0x1E, 0x01, 0x01, 0x11, 0x0E}, // 5
	{0x0E, 0x10, 0x1E, 0x11, 0x11, 0x11, 0x0E}, // 6
	{0x1F, 0x01, 0x02, 0x04, 0x04, 0x04, 0x04}, // 7
	{0x0E, 0x11, 0x11, 0x0E, 0x11, 0x11, 0x0E}, // 8
	{0x0E, 0x11, 0x11, 0x0F, 0x01, 0x02, 0x0E}, // 9
}

func drawGlyph(img *image.NRGBA, bx image.Rectangle, x, y, digit int, fg color.NRGBA) {
	glyph := digitFont[digit]
	shifts := [5]uint8{4, 3, 2, 1, 0}
	for row := 0; row < 7; row++ {
		for col := 0; col < 5; col++ {
			if glyph[row]&(1<<shifts[col]) != 0 {
				setClipped(img, bx, x+col, y+row, fg)
			}
		}
	}
}

func setClipped(img *image.NRGBA, bx image.Rectangle, x, y int, c color.NRGBA) {
	if x >= bx.Min.X && x < bx.Max.X && y >= bx.Min.Y && y < bx.Max.Y {
		img.SetNRGBA(x, y, c)
	}
}

func minF(vals ...float64) float64 {
	m := vals[0]
	for _, v := range vals[1:] {
		if v < m {
			m = v
		}
	}
	return m
}

func maxF(vals ...float64) float64 {
	m := vals[0]
	for _, v := range vals[1:] {
		if v > m {
			m = v
		}
	}
	return m
}
