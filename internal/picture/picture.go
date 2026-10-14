// Package picture turns an image or an animated GIF into Raw Mode frames for
// a matrix of a given size: decoded with the standard library, fitted into
// the matrix (aspect ratio kept, centered on black) with a box filter, and
// quantised to 8-bit RGB.
package picture

import (
	"bytes"
	"fmt"
	"image"
	"image/draw"
	"image/gif"
	"time"

	// Formats Decode accepts besides GIF.
	_ "image/jpeg"
	_ "image/png"
)

// MaxPixels bounds the canvas Decode accepts, so a hostile header can't make
// it allocate gigabytes.
const MaxPixels = 4096 * 4096

// Picture is a decoded still image or animation, at its own size.
type Picture struct {
	still     image.Image
	animation *gif.GIF
}

// Frame is one frame for the matrix: RGB bytes, row-major from the top-left,
// and how long it shows.
type Frame struct {
	RGB   []byte
	Delay time.Duration
}

// Decode reads a PNG, JPEG or GIF.
func Decode(data []byte) (*Picture, error) {
	cfg, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("not a PNG, JPEG or GIF: %w", err)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 || cfg.Width*cfg.Height > MaxPixels {
		return nil, fmt.Errorf("%s of %dx%d: too large", format, cfg.Width, cfg.Height)
	}
	if format == "gif" {
		g, err := gif.DecodeAll(bytes.NewReader(data))
		if err != nil {
			return nil, fmt.Errorf("decode gif: %w", err)
		}
		if len(g.Image) > 1 {
			return &Picture{animation: g}, nil
		}
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", format, err)
	}
	return &Picture{still: img}, nil
}

// Frames renders the picture for a width x height matrix: one frame for a
// still image, every frame of an animation with its delay.
func (p *Picture) Frames(width, height int) []Frame {
	if p.animation == nil {
		return []Frame{{RGB: fit(p.still, width, height)}}
	}
	g := p.animation
	// Each GIF frame draws over the canvas the previous ones left, as its
	// disposal method says.
	canvas := image.NewRGBA(image.Rect(0, 0, g.Config.Width, g.Config.Height))
	frames := make([]Frame, len(g.Image))
	for i, img := range g.Image {
		var previous *image.RGBA
		disposal := byte(0)
		if i < len(g.Disposal) {
			disposal = g.Disposal[i]
		}
		if disposal == gif.DisposalPrevious {
			previous = image.NewRGBA(canvas.Bounds())
			copy(previous.Pix, canvas.Pix)
		}
		draw.Draw(canvas, img.Bounds(), img, img.Bounds().Min, draw.Over)
		frames[i] = Frame{RGB: fit(canvas, width, height), Delay: delay(g, i)}
		switch disposal {
		case gif.DisposalBackground:
			// Back to transparent: an unlit LED, as browsers show it.
			draw.Draw(canvas, img.Bounds(), image.Transparent, image.Point{}, draw.Src)
		case gif.DisposalPrevious:
			canvas = previous
		}
	}
	return frames
}

// MinDelay is the shortest frame delay played as given. Shorter ones (0 or
// 10 ms) play at DefaultDelay, as browsers do: such GIFs were made for that.
const (
	MinDelay     = 20 * time.Millisecond
	DefaultDelay = 100 * time.Millisecond
)

// delay is how long frame i of g shows.
func delay(g *gif.GIF, i int) time.Duration {
	if i >= len(g.Delay) {
		return DefaultDelay
	}
	d := time.Duration(g.Delay[i]) * 10 * time.Millisecond
	if d < MinDelay {
		return DefaultDelay
	}
	return d
}

// fit scales img into a width x height matrix, keeping its aspect ratio and
// centering it on black. Each LED is the average of the source pixels it
// covers (at least the nearest one, when scaling up). Transparency shows as
// black: an unlit LED.
func fit(img image.Image, width, height int) []byte {
	b := img.Bounds()
	sw, sh := b.Dx(), b.Dy()
	// The fitted size: the largest that keeps the aspect ratio.
	tw, th := width, sh*width/sw
	if th > height {
		tw, th = sw*height/sh, height
	}
	tw, th = max(tw, 1), max(th, 1)
	ox, oy := (width-tw)/2, (height-th)/2

	rgb := make([]byte, width*height*3)
	for ty := range th {
		y0 := ty * sh / th
		y1 := max((ty+1)*sh/th, y0+1)
		for tx := range tw {
			x0 := tx * sw / tw
			x1 := max((tx+1)*sw/tw, x0+1)
			var r, g, bl, n uint64
			for y := y0; y < y1; y++ {
				for x := x0; x < x1; x++ {
					// Alpha-premultiplied, so this is the pixel over black.
					pr, pg, pb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
					r, g, bl, n = r+uint64(pr), g+uint64(pg), bl+uint64(pb), n+1
				}
			}
			i := ((oy+ty)*width + ox + tx) * 3
			rgb[i], rgb[i+1], rgb[i+2] = to8(r, n), to8(g, n), to8(bl, n)
		}
	}
	return rgb
}

// to8 is the 8-bit channel of the average of n 16-bit samples summing to sum.
func to8(sum, n uint64) byte {
	return byte((sum/n + 128) / 257)
}
