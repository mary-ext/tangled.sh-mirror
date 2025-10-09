// Copyright 2024 The Forgejo Authors. All rights reserved.
// Copyright 2025 The Tangled Authors -- repurposed for Tangled use.
// SPDX-License-Identifier: MIT

package ogcard

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"io"
	"log"
	"math"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/goki/freetype"
	"github.com/goki/freetype/truetype"
	"github.com/srwiley/oksvg"
	"github.com/srwiley/rasterx"
	"golang.org/x/image/draw"
	"golang.org/x/image/font"
	"tangled.org/core/appview/pages"

	_ "golang.org/x/image/webp" // for processing webp images
)

type Card struct {
	Img    *image.RGBA
	Font   *truetype.Font
	Margin int
	Width  int
	Height int
}

var fontCache = sync.OnceValues(func() (*truetype.Font, error) {
	interVar, err := pages.Files.ReadFile("static/fonts/InterVariable.ttf")
	if err != nil {
		return nil, err
	}
	return truetype.Parse(interVar)
})

// DefaultSize returns the default size for a card
func DefaultSize() (int, int) {
	return 1200, 630
}

// NewCard creates a new card with the given dimensions in pixels
func NewCard(width, height int) (*Card, error) {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)

	font, err := fontCache()
	if err != nil {
		return nil, err
	}

	return &Card{
		Img:    img,
		Font:   font,
		Margin: 0,
		Width:  width,
		Height: height,
	}, nil
}

// Split splits the card horizontally or vertically by a given percentage; the first card returned has the percentage
// size, and the second card has the remainder.  Both cards draw to a subsection of the same image buffer.
func (c *Card) Split(vertical bool, percentage int) (*Card, *Card) {
	bounds := c.Img.Bounds()
	bounds = image.Rect(bounds.Min.X+c.Margin, bounds.Min.Y+c.Margin, bounds.Max.X-c.Margin, bounds.Max.Y-c.Margin)
	if vertical {
		mid := (bounds.Dx() * percentage / 100) + bounds.Min.X
		subleft := c.Img.SubImage(image.Rect(bounds.Min.X, bounds.Min.Y, mid, bounds.Max.Y)).(*image.RGBA)
		subright := c.Img.SubImage(image.Rect(mid, bounds.Min.Y, bounds.Max.X, bounds.Max.Y)).(*image.RGBA)
		return &Card{Img: subleft, Font: c.Font, Width: subleft.Bounds().Dx(), Height: subleft.Bounds().Dy()},
			&Card{Img: subright, Font: c.Font, Width: subright.Bounds().Dx(), Height: subright.Bounds().Dy()}
	}
	mid := (bounds.Dy() * percentage / 100) + bounds.Min.Y
	subtop := c.Img.SubImage(image.Rect(bounds.Min.X, bounds.Min.Y, bounds.Max.X, mid)).(*image.RGBA)
	subbottom := c.Img.SubImage(image.Rect(bounds.Min.X, mid, bounds.Max.X, bounds.Max.Y)).(*image.RGBA)
	return &Card{Img: subtop, Font: c.Font, Width: subtop.Bounds().Dx(), Height: subtop.Bounds().Dy()},
		&Card{Img: subbottom, Font: c.Font, Width: subbottom.Bounds().Dx(), Height: subbottom.Bounds().Dy()}
}

// SetMargin sets the margins for the card
func (c *Card) SetMargin(margin int) {
	c.Margin = margin
}

type (
	VAlign int64
	HAlign int64
)

const (
	Top VAlign = iota
	Middle
	Bottom
)

const (
	Left HAlign = iota
	Center
	Right
)

// DrawText draws text within the card, respecting margins and alignment
func (c *Card) DrawText(text string, textColor color.Color, sizePt float64, valign VAlign, halign HAlign) ([]string, error) {
	ft := freetype.NewContext()
	ft.SetDPI(72)
	ft.SetFont(c.Font)
	ft.SetFontSize(sizePt)
	ft.SetClip(c.Img.Bounds())
	ft.SetDst(c.Img)
	ft.SetSrc(image.NewUniform(textColor))

	face := truetype.NewFace(c.Font, &truetype.Options{Size: sizePt, DPI: 72})
	fontHeight := ft.PointToFixed(sizePt).Ceil()

	bounds := c.Img.Bounds()
	bounds = image.Rect(bounds.Min.X+c.Margin, bounds.Min.Y+c.Margin, bounds.Max.X-c.Margin, bounds.Max.Y-c.Margin)
	boxWidth, boxHeight := bounds.Size().X, bounds.Size().Y
	// draw.Draw(c.Img, bounds, image.NewUniform(color.Gray{128}), image.Point{}, draw.Src) // Debug draw box

	// Try to apply wrapping to this text; we'll find the most text that will fit into one line, record that line, move
	// on.  We precalculate each line before drawing so that we can support valign="middle" correctly which requires
	// knowing the total height, which is related to how many lines we'll have.
	lines := make([]string, 0)
	textWords := strings.Split(text, " ")
	currentLine := ""
	heightTotal := 0

	for {
		if len(textWords) == 0 {
			// Ran out of words.
			if currentLine != "" {
				heightTotal += fontHeight
				lines = append(lines, currentLine)
			}
			break
		}

		nextWord := textWords[0]
		proposedLine := currentLine
		if proposedLine != "" {
			proposedLine += " "
		}
		proposedLine += nextWord

		proposedLineWidth := font.MeasureString(face, proposedLine)
		if proposedLineWidth.Ceil() > boxWidth {
			// no, proposed line is too big; we'll use the last "currentLine"
			heightTotal += fontHeight
			if currentLine != "" {
				lines = append(lines, currentLine)
				currentLine = ""
				// leave nextWord in textWords and keep going
			} else {
				// just nextWord by itself doesn't fit on a line; well, we can't skip it, but we'll consume it
				// regardless as a line by itself.  It will be clipped by the drawing routine.
				lines = append(lines, nextWord)
				textWords = textWords[1:]
			}
		} else {
			// yes, it will fit
			currentLine = proposedLine
			textWords = textWords[1:]
		}
	}

	textY := 0
	switch valign {
	case Top:
		textY = fontHeight
	case Bottom:
		textY = boxHeight - heightTotal + fontHeight
	case Middle:
		textY = ((boxHeight - heightTotal) / 2) + fontHeight
	}

	for _, line := range lines {
		lineWidth := font.MeasureString(face, line)

		textX := 0
		switch halign {
		case Left:
			textX = 0
		case Right:
			textX = boxWidth - lineWidth.Ceil()
		case Center:
			textX = (boxWidth - lineWidth.Ceil()) / 2
		}

		pt := freetype.Pt(bounds.Min.X+textX, bounds.Min.Y+textY)
		_, err := ft.DrawString(line, pt)
		if err != nil {
			return nil, err
		}

		textY += fontHeight
	}

	return lines, nil
}

// DrawTextAt draws text at a specific position with the given alignment
func (c *Card) DrawTextAt(text string, x, y int, textColor color.Color, sizePt float64, valign VAlign, halign HAlign) error {
	_, err := c.DrawTextAtWithWidth(text, x, y, textColor, sizePt, valign, halign)
	return err
}

// DrawTextAtWithWidth draws text at a specific position and returns the text width
func (c *Card) DrawTextAtWithWidth(text string, x, y int, textColor color.Color, sizePt float64, valign VAlign, halign HAlign) (int, error) {
	ft := freetype.NewContext()
	ft.SetDPI(72)
	ft.SetFont(c.Font)
	ft.SetFontSize(sizePt)
	ft.SetClip(c.Img.Bounds())
	ft.SetDst(c.Img)
	ft.SetSrc(image.NewUniform(textColor))

	face := truetype.NewFace(c.Font, &truetype.Options{Size: sizePt, DPI: 72})
	fontHeight := ft.PointToFixed(sizePt).Ceil()
	lineWidth := font.MeasureString(face, text)
	textWidth := lineWidth.Ceil()

	// Adjust position based on alignment
	adjustedX := x
	adjustedY := y

	switch halign {
	case Left:
		// x is already at the left position
	case Right:
		adjustedX = x - textWidth
	case Center:
		adjustedX = x - textWidth/2
	}

	switch valign {
	case Top:
		adjustedY = y + fontHeight
	case Bottom:
		adjustedY = y
	case Middle:
		adjustedY = y + fontHeight/2
	}

	pt := freetype.Pt(adjustedX, adjustedY)
	_, err := ft.DrawString(text, pt)
	return textWidth, err
}

// DrawBoldText draws bold text by rendering multiple times with slight offsets
func (c *Card) DrawBoldText(text string, x, y int, textColor color.Color, sizePt float64, valign VAlign, halign HAlign) (int, error) {
	// Draw the text multiple times with slight offsets to create bold effect
	offsets := []struct{ dx, dy int }{
		{0, 0}, // original
		{1, 0}, // right
		{0, 1}, // down
		{1, 1}, // diagonal
	}

	var width int
	for _, offset := range offsets {
		w, err := c.DrawTextAtWithWidth(text, x+offset.dx, y+offset.dy, textColor, sizePt, valign, halign)
		if err != nil {
			return 0, err
		}
		if width == 0 {
			width = w
		}
	}
	return width, nil
}

// DrawSVGIcon draws an SVG icon from the embedded files at the specified position
func (c *Card) DrawSVGIcon(svgPath string, x, y, size int, iconColor color.Color) error {
	svgData, err := pages.Files.ReadFile(svgPath)
	if err != nil {
		return fmt.Errorf("failed to read SVG file %s: %w", svgPath, err)
	}

	// Convert color to hex string for SVG
	rgba, isRGBA := iconColor.(color.RGBA)
	if !isRGBA {
		r, g, b, a := iconColor.RGBA()
		rgba = color.RGBA{uint8(r >> 8), uint8(g >> 8), uint8(b >> 8), uint8(a >> 8)}
	}
	colorHex := fmt.Sprintf("#%02x%02x%02x", rgba.R, rgba.G, rgba.B)

	// Replace currentColor with our desired color in the SVG
	svgString := string(svgData)
	svgString = strings.ReplaceAll(svgString, "currentColor", colorHex)

	// Make the stroke thicker
	svgString = strings.ReplaceAll(svgString, `stroke-width="2"`, `stroke-width="3"`)

	// Parse SVG
	icon, err := oksvg.ReadIconStream(strings.NewReader(svgString))
	if err != nil {
		return fmt.Errorf("failed to parse SVG %s: %w", svgPath, err)
	}

	// Set the icon size
	w, h := float64(size), float64(size)
	icon.SetTarget(0, 0, w, h)

	// Create a temporary RGBA image for the icon
	iconImg := image.NewRGBA(image.Rect(0, 0, size, size))

	// Create scanner and rasterizer
	scanner := rasterx.NewScannerGV(size, size, iconImg, iconImg.Bounds())
	raster := rasterx.NewDasher(size, size, scanner)

	// Draw the icon
	icon.Draw(raster, 1.0)

	// Draw the icon onto the card at the specified position
	bounds := c.Img.Bounds()
	destRect := image.Rect(x, y, x+size, y+size)

	// Make sure we don't draw outside the card bounds
	if destRect.Max.X > bounds.Max.X {
		destRect.Max.X = bounds.Max.X
	}
	if destRect.Max.Y > bounds.Max.Y {
		destRect.Max.Y = bounds.Max.Y
	}

	draw.Draw(c.Img, destRect, iconImg, image.Point{}, draw.Over)

	return nil
}

// DrawImage fills the card with an image, scaled to maintain the original aspect ratio and centered with respect to the non-filled dimension
func (c *Card) DrawImage(img image.Image) {
	bounds := c.Img.Bounds()
	targetRect := image.Rect(bounds.Min.X+c.Margin, bounds.Min.Y+c.Margin, bounds.Max.X-c.Margin, bounds.Max.Y-c.Margin)
	srcBounds := img.Bounds()
	srcAspect := float64(srcBounds.Dx()) / float64(srcBounds.Dy())
	targetAspect := float64(targetRect.Dx()) / float64(targetRect.Dy())

	var scale float64
	if srcAspect > targetAspect {
		// Image is wider than target, scale by width
		scale = float64(targetRect.Dx()) / float64(srcBounds.Dx())
	} else {
		// Image is taller or equal, scale by height
		scale = float64(targetRect.Dy()) / float64(srcBounds.Dy())
	}

	newWidth := int(math.Round(float64(srcBounds.Dx()) * scale))
	newHeight := int(math.Round(float64(srcBounds.Dy()) * scale))

	// Center the image within the target rectangle
	offsetX := (targetRect.Dx() - newWidth) / 2
	offsetY := (targetRect.Dy() - newHeight) / 2

	scaledRect := image.Rect(targetRect.Min.X+offsetX, targetRect.Min.Y+offsetY, targetRect.Min.X+offsetX+newWidth, targetRect.Min.Y+offsetY+newHeight)
	draw.CatmullRom.Scale(c.Img, scaledRect, img, srcBounds, draw.Over, nil)
}

func fallbackImage() image.Image {
	// can't usage image.Uniform(color.White) because it's infinitely sized causing a panic in the scaler in DrawImage
	img := image.NewRGBA(image.Rect(0, 0, 1, 1))
	img.Set(0, 0, color.White)
	return img
}

// As defensively as possible, attempt to load an image from a presumed external and untrusted URL
func (c *Card) fetchExternalImage(url string) (image.Image, bool) {
	// Use a short timeout; in the event of any failure we'll be logging and returning a placeholder, but we don't want
	// this rendering process to be slowed down
	client := &http.Client{
		Timeout: 1 * time.Second, // 1 second timeout
	}

	resp, err := client.Get(url)
	if err != nil {
		log.Printf("error when fetching external image from %s: %v", url, err)
		return nil, false
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		log.Printf("non-OK error code when fetching external image from %s: %s", url, resp.Status)
		return nil, false
	}

	contentType := resp.Header.Get("Content-Type")
	// Support content types are in-sync with the allowed custom avatar file types
	if contentType != "image/png" && contentType != "image/jpeg" && contentType != "image/gif" && contentType != "image/webp" {
		log.Printf("fetching external image returned unsupported Content-Type which was ignored: %s", contentType)
		return nil, false
	}

	body := resp.Body
	bodyBytes, err := io.ReadAll(body)
	if err != nil {
		log.Printf("error when fetching external image from %s: %v", url, err)
		return nil, false
	}

	bodyBuffer := bytes.NewReader(bodyBytes)
	_, imgType, err := image.DecodeConfig(bodyBuffer)
	if err != nil {
		log.Printf("error when decoding external image from %s: %v", url, err)
		return nil, false
	}

	// Verify that we have a match between actual data understood in the image body and the reported Content-Type
	if (contentType == "image/png" && imgType != "png") ||
		(contentType == "image/jpeg" && imgType != "jpeg") ||
		(contentType == "image/gif" && imgType != "gif") ||
		(contentType == "image/webp" && imgType != "webp") {
		log.Printf("while fetching external image, mismatched image body (%s) and Content-Type (%s)", imgType, contentType)
		return nil, false
	}

	_, err = bodyBuffer.Seek(0, io.SeekStart) // reset for actual decode
	if err != nil {
		log.Printf("error w/ bodyBuffer.Seek")
		return nil, false
	}
	img, _, err := image.Decode(bodyBuffer)
	if err != nil {
		log.Printf("error when decoding external image from %s: %v", url, err)
		return nil, false
	}

	return img, true
}

func (c *Card) DrawExternalImage(url string) {
	image, ok := c.fetchExternalImage(url)
	if !ok {
		image = fallbackImage()
	}
	c.DrawImage(image)
}

// DrawCircularExternalImage draws an external image as a circle at the specified position
func (c *Card) DrawCircularExternalImage(url string, x, y, size int) error {
	img, ok := c.fetchExternalImage(url)
	if !ok {
		img = fallbackImage()
	}

	// Create a circular mask
	circle := image.NewRGBA(image.Rect(0, 0, size, size))
	center := size / 2
	radius := float64(size / 2)

	// Scale the source image to fit the circle
	srcBounds := img.Bounds()
	scaledImg := image.NewRGBA(image.Rect(0, 0, size, size))
	draw.CatmullRom.Scale(scaledImg, scaledImg.Bounds(), img, srcBounds, draw.Src, nil)

	// Draw the image with circular clipping
	for cy := 0; cy < size; cy++ {
		for cx := 0; cx < size; cx++ {
			// Calculate distance from center
			dx := float64(cx - center)
			dy := float64(cy - center)
			distance := math.Sqrt(dx*dx + dy*dy)

			// Only draw pixels within the circle
			if distance <= radius {
				circle.Set(cx, cy, scaledImg.At(cx, cy))
			}
		}
	}

	// Draw the circle onto the card
	bounds := c.Img.Bounds()
	destRect := image.Rect(x, y, x+size, y+size)

	// Make sure we don't draw outside the card bounds
	if destRect.Max.X > bounds.Max.X {
		destRect.Max.X = bounds.Max.X
	}
	if destRect.Max.Y > bounds.Max.Y {
		destRect.Max.Y = bounds.Max.Y
	}

	draw.Draw(c.Img, destRect, circle, image.Point{}, draw.Over)

	return nil
}

// DrawRect draws a rect with the given color
func (c *Card) DrawRect(startX, startY, endX, endY int, color color.Color) {
	draw.Draw(c.Img, image.Rect(startX, startY, endX, endY), &image.Uniform{color}, image.Point{}, draw.Src)
}
