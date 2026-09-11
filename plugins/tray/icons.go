package tray

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"

	"golang.org/x/image/font"
	"golang.org/x/image/font/basicfont"
	"golang.org/x/image/math/fixed"
)

const iconSize = 22

type iconKey string

const (
	iconIdle       iconKey = "idle"
	iconConnecting iconKey = "connecting"
	iconRecording  iconKey = "recording"
	iconStopping   iconKey = "stopping"
	iconError      iconKey = "error"
	iconEnter      iconKey = "enter"
	iconUndo       iconKey = "undo"
)

var iconColors = map[iconKey]color.RGBA{
	iconIdle:       {145, 145, 145, 255},
	iconConnecting: {245, 190, 70, 255},
	iconRecording:  {255, 65, 65, 255},
	iconStopping:   {255, 160, 70, 255},
	iconError:      {200, 30, 30, 255},
	iconEnter:      {80, 210, 120, 255},
	iconUndo:       {255, 170, 60, 255},
}

var iconLetters = map[iconKey]string{
	iconIdle:       "I",
	iconConnecting: "C",
	iconRecording:  "R",
	iconStopping:   "S",
	iconError:      "E",
	iconEnter:      "↵",
	iconUndo:       "U",
}

var iconCache = map[iconKey][]byte{}

func iconFor(state string) ([]byte, error) {
	key, ok := mapState(state)
	if !ok {
		key = iconIdle
	}
	if b, ok := iconCache[key]; ok {
		return b, nil
	}
	b, err := renderIcon(iconColors[key], iconLetters[key])
	if err != nil {
		return nil, err
	}
	iconCache[key] = b
	return b, nil
}

func mapState(state string) (iconKey, bool) {
	switch state {
	case "connecting":
		return iconConnecting, true
	case "recording":
		return iconRecording, true
	case "stopping", "stopping_delayed":
		return iconStopping, true
	case "error":
		return iconError, true
	case "enter":
		return iconEnter, true
	case "undo":
		return iconUndo, true
	case "idle", "":
		return iconIdle, true
	default:
		return iconIdle, false
	}
}

func renderIcon(bg color.RGBA, letter string) ([]byte, error) {
	img := image.NewRGBA(image.Rect(0, 0, iconSize, iconSize))
	draw.Draw(img, img.Bounds(), &image.Uniform{C: bg}, image.Point{}, draw.Src)
	drawBorder(img, bg)
	if letter != "" {
		drawLetter(img, letter)
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("encode png: %w", err)
	}
	return buf.Bytes(), nil
}

func drawBorder(img *image.RGBA, bg color.RGBA) {
	dark := color.RGBA{
		R: bg.R / 2,
		G: bg.G / 2,
		B: bg.B / 2,
		A: 255,
	}
	for x := 0; x < iconSize; x++ {
		img.Set(x, 0, dark)
		img.Set(x, iconSize-1, dark)
	}
	for y := 0; y < iconSize; y++ {
		img.Set(0, y, dark)
		img.Set(iconSize-1, y, dark)
	}
}

func drawLetter(img *image.RGBA, letter string) {
	face := basicfont.Face7x13
	adv := font.MeasureString(face, letter)
	width := adv.Round()
	x := (iconSize - width) / 2
	y := (iconSize - 13) / 2
	if x < 1 {
		x = 1
	}
	if y < 1 {
		y = 1
	}
	d := &font.Drawer{
		Dst:  img,
		Src:  image.NewUniform(color.White),
		Face: face,
		Dot:  fixed.P(x, y+12),
	}
	d.DrawString(letter)
}
