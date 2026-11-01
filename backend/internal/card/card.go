// Package card draws pictures for stories: a black background with a glow,
// amounts in gold, Savik and the address of the bot. It is pure Go on purpose:
// the backend image has no shell, no libc and no way to run a renderer, and
// embedded fonts and poses keep it that way.
package card

import (
	"bytes"
	"embed"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"strings"
	"sync"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

//go:embed assets
var assets embed.FS

const (
	width  = 1080
	height = 1920
	margin = 96

	poseSize = 620
	dpi      = 72
)

// Pose is a picture of Savik.
type Pose string

const (
	PoseNeutral Pose = "01-neutral"
	PoseJoy     Pose = "03-joy"
	PoseProud   Pose = "09-proud"
	PoseFull    Pose = "20-full"
	PoseSaving  Pose = "16-hide-coin"
)

// Glow is the color of the light behind Savik.
type Glow int

const (
	GlowBlue Glow = iota
	GlowOrange
)

// Card is what a picture says. Everything but Value may be empty.
type Card struct {
	Pose    Pose
	Glow    Glow
	Eyebrow string   // a small line on top: what this is about
	Value   string   // the big one in gold: "50%", "7 дней"
	Title   string   // what the value is about
	Lines   []string // details, in gray
	Footer  string   // the address of the bot: stories must lead somewhere
}

var (
	white = color.RGBA{R: 0xff, G: 0xff, B: 0xff, A: 0xff}
	gray  = color.RGBA{R: 0xa8, G: 0xad, B: 0xb8, A: 0xff}
	gold  = color.RGBA{R: 0xf5, G: 0xc4, B: 0x5a, A: 0xff}
)

var (
	loadFonts = sync.OnceValues(func() (*fonts, error) {
		regular, err := parseFont("assets/onest-regular.ttf")
		if err != nil {
			return nil, err
		}
		bold, err := parseFont("assets/onest-bold.ttf")
		if err != nil {
			return nil, err
		}
		return &fonts{regular: regular, bold: bold}, nil
	})
	loadedPoses sync.Map // Pose -> image.Image
)

type fonts struct{ regular, bold *opentype.Font }

func parseFont(path string) (*opentype.Font, error) {
	data, err := assets.ReadFile(path)
	if err != nil {
		return nil, err
	}
	f, err := opentype.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return f, nil
}

func pose(p Pose) (image.Image, error) {
	if img, ok := loadedPoses.Load(p); ok {
		return img.(image.Image), nil
	}
	data, err := assets.ReadFile("assets/" + string(p) + ".png")
	if err != nil {
		return nil, err
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("pose %s: %w", p, err)
	}
	loadedPoses.Store(p, img)
	return img, nil
}

// Render draws the card as a PNG.
func Render(c Card) ([]byte, error) {
	f, err := loadFonts()
	if err != nil {
		return nil, err
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	paintGlow(img, c.Glow)

	y := 190
	if c.Eyebrow != "" {
		y = drawCentered(img, f.bold, c.Eyebrow, 46, gray, y)
	}
	if c.Pose != "" {
		p, err := pose(c.Pose)
		if err != nil {
			return nil, err
		}
		left := (width - poseSize) / 2
		xdraw.CatmullRom.Scale(img, image.Rect(left, y+40, left+poseSize, y+40+poseSize), p, p.Bounds(), xdraw.Over, nil)
		y += 40 + poseSize + 40
	}
	if c.Value != "" {
		y = drawFitted(img, f.bold, c.Value, 170, gold, y+110)
	}
	if c.Title != "" {
		y = drawWrapped(img, f.bold, c.Title, 62, white, y+70)
	}
	y += 30
	for _, line := range c.Lines {
		y = drawWrapped(img, f.regular, line, 46, gray, y+30)
	}
	if c.Footer != "" {
		drawCentered(img, f.regular, c.Footer, 40, gray, height-130)
	}

	var out bytes.Buffer
	enc := png.Encoder{CompressionLevel: png.BestSpeed}
	if err := enc.Encode(&out, img); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// paintGlow fills the card with black and a soft light from behind Savik.
func paintGlow(img *image.RGBA, g Glow) {
	glow := [3]float64{38, 92, 255}
	if g == GlowOrange {
		glow = [3]float64{255, 122, 26}
	}
	const cx, cy, radius = width / 2.0, 640.0, 900.0
	for y := range height {
		for x := range width {
			d := math.Hypot(float64(x)-cx, float64(y)-cy) / radius
			light := math.Max(0, 1-d)
			light = 0.62 * light * light
			img.SetRGBA(x, y, color.RGBA{
				R: uint8(8 + glow[0]*light), G: uint8(8 + glow[1]*light), B: uint8(12 + glow[2]*light), A: 0xff,
			})
		}
	}
}

func face(f *opentype.Font, size float64) (font.Face, error) {
	return opentype.NewFace(f, &opentype.FaceOptions{Size: size, DPI: dpi, Hinting: font.HintingNone})
}

func drawText(img *image.RGBA, face font.Face, text string, c color.Color, x, baseline int) {
	d := font.Drawer{Dst: img, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, baseline)}
	d.DrawString(text)
}

func centered(face font.Face, text string) int {
	return (width - font.MeasureString(face, text).Round()) / 2
}

// drawCentered puts one line with its baseline under y and returns the new y.
func drawCentered(img *image.RGBA, f *opentype.Font, text string, size float64, c color.Color, y int) int {
	fc, err := face(f, size)
	if err != nil {
		return y
	}
	defer func() { _ = fc.Close() }()
	baseline := y + int(size)
	drawText(img, fc, text, c, centered(fc, text), baseline)
	return baseline
}

// drawFitted is drawCentered with the size reduced until the line fits.
func drawFitted(img *image.RGBA, f *opentype.Font, text string, size float64, c color.Color, y int) int {
	for size > 60 {
		fc, err := face(f, size)
		if err != nil {
			return y
		}
		w := font.MeasureString(fc, text).Round()
		_ = fc.Close()
		if w <= width-2*margin {
			break
		}
		size -= 10
	}
	return drawCentered(img, f, text, size, c, y)
}

// drawWrapped breaks the text into centered lines.
func drawWrapped(img *image.RGBA, f *opentype.Font, text string, size float64, c color.Color, y int) int {
	fc, err := face(f, size)
	if err != nil {
		return y
	}
	defer func() { _ = fc.Close() }()
	step := int(size * 1.3)
	for i, line := range wrap(fc, text, width-2*margin) {
		if i > 0 {
			y += step - int(size)
		}
		y += int(size)
		drawText(img, fc, line, c, centered(fc, line), y)
	}
	return y
}

func wrap(fc font.Face, text string, limit int) []string {
	var lines []string
	line := ""
	for _, word := range strings.Fields(text) {
		next := word
		if line != "" {
			next = line + " " + word
		}
		if line != "" && font.MeasureString(fc, next).Round() > limit {
			lines = append(lines, line)
			line = word
			continue
		}
		line = next
	}
	if line != "" {
		lines = append(lines, line)
	}
	return lines
}
