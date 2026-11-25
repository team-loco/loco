//go:build darwin || linux

package main

import (
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"runtime"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/vt"
	"golang.org/x/image/font"
	"golang.org/x/image/font/gofont/gomono"
	"golang.org/x/image/font/gofont/gomonobold"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type terminalTheme struct {
	background, foreground color.Color
	colorFGBG              string
}

func themeFor(name string) terminalTheme {
	if name == lightTheme {
		return terminalTheme{color.RGBA{255, 255, 255, 255}, color.RGBA{42, 37, 34, 255}, "0;15"}
	}
	return terminalTheme{color.RGBA{20, 20, 20, 255}, color.RGBA{243, 239, 234, 255}, "15;0"}
}

const (
	fontSize   = 19
	cellHeight = 28
	padding    = 24
)

func savePNG(path string, screen *vt.Emulator, theme terminalTheme) (resultErr error) {
	img, err := render(screen, theme)
	if err != nil {
		return err
	}
	file, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() {
		resultErr = errors.Join(resultErr, file.Close())
		if resultErr != nil {
			resultErr = errors.Join(resultErr, os.Remove(path))
		}
	}()
	return png.Encode(file, img)
}

type faces struct {
	normal, bold, symbols font.Face
}

func loadFaces() (faces, error) {
	var normal, bold, symbols font.Face
	var err error
	if runtime.GOOS == "darwin" {
		normal, err = fileFace("/System/Library/Fonts/Menlo.ttc", 0)
		if err == nil {
			bold, err = fileFace("/System/Library/Fonts/Menlo.ttc", 1)
		}
		if err == nil {
			symbols, err = fileFace("/System/Library/Fonts/Apple Symbols.ttf", 0)
		}
	} else {
		normal, err = fileFace("/usr/share/fonts/truetype/dejavu/DejaVuSansMono.ttf", 0)
		if err == nil {
			bold, err = fileFace("/usr/share/fonts/truetype/dejavu/DejaVuSansMono-Bold.ttf", 0)
		}
		symbols = normal
	}
	if err == nil {
		return faces{normal, bold, symbols}, nil
	}
	// Keep the renderer usable on machines without the preferred system fonts.
	normal, err = fontFace(gomono.TTF, 0)
	if err != nil {
		return faces{}, err
	}
	bold, err = fontFace(gomonobold.TTF, 0)
	if err != nil {
		return faces{}, err
	}
	return faces{normal, bold, normal}, nil
}

func fileFace(path string, index int) (font.Face, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return fontFace(data, index)
}

func fontFace(data []byte, index int) (font.Face, error) {
	collection, err := opentype.ParseCollection(data)
	if err != nil {
		return nil, err
	}
	f, err := collection.Font(index)
	if err != nil {
		return nil, err
	}
	return opentype.NewFace(f, &opentype.FaceOptions{Size: fontSize, DPI: 72, Hinting: font.HintingNone})
}

func render(screen *vt.Emulator, theme terminalTheme) (*image.RGBA, error) {
	fs, err := loadFaces()
	if err != nil {
		return nil, fmt.Errorf("load terminal fonts: %w", err)
	}
	cellWidth := font.MeasureString(fs.normal, "M").Round()
	canvas := image.NewRGBA(image.Rect(0, 0, screen.Width()*cellWidth+2*padding, screen.Height()*cellHeight+2*padding))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(theme.background), image.Point{}, draw.Src)

	for y := range screen.Height() {
		for x := range screen.Width() {
			cell := screen.CellAt(x, y)
			if cell == nil || cell.Width == 0 {
				continue
			}
			ink, paper := cellColors(cell, theme)
			left, top := padding+x*cellWidth, padding+y*cellHeight
			width := cell.Width * cellWidth
			rect := image.Rect(left, top, left+width, top+cellHeight)
			draw.Draw(canvas, rect, image.NewUniform(paper), image.Point{}, draw.Src)
			if cell.Content == "" || cell.Content == " " || cell.Style.Attrs&uv.AttrConceal != 0 {
				continue
			}
			face := fs.normal
			if cell.Style.Attrs&uv.AttrBold != 0 {
				face = fs.bold
			}
			// Menlo lacks the braille characters used in the CLI's spinner.
			for _, r := range cell.Content {
				if r >= 0x2800 && r <= 0x28ff {
					face = fs.symbols
					break
				}
			}
			d := font.Drawer{
				Dst:  canvas,
				Src:  image.NewUniform(ink),
				Face: face,
				Dot:  fixed.P(left, top+cellHeight-7),
			}
			d.DrawString(cell.Content)
			if cell.Style.Underline != ansi.UnderlineNone {
				line := image.Rect(left, top+cellHeight-4, left+width, top+cellHeight-3)
				draw.Draw(canvas, line, image.NewUniform(ink), image.Point{}, draw.Src)
			}
		}
	}
	return canvas, nil
}

func cellColors(cell *uv.Cell, theme terminalTheme) (ink, paper color.Color) {
	ink, paper = cell.Style.Fg, cell.Style.Bg
	if ink == nil {
		ink = theme.foreground
	}
	if paper == nil {
		paper = theme.background
	}
	if cell.Style.Attrs&uv.AttrReverse != 0 {
		ink, paper = paper, ink
	}
	return ink, paper
}
