// Package raster produces deterministic, supersampled PNG exposures.
package raster

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"math"
	"prismplate/internal/geom"
	"prismplate/internal/paint"
	"prismplate/internal/scene"
)

// Canvas is the deterministic linear-light, premultiplied sample buffer that
// both the PNG encoder and page imposition consume. Every pixel is the average
// of four subpixel samples, so rasterizing once and resampling later is exact.
type Canvas struct {
	Width  int
	Height int
	Pix    []paint.Pixel
}

func (c *Canvas) At(x, y int) paint.Pixel {
	if x < 0 || y < 0 || x >= c.Width || y >= c.Height {
		return paint.Pixel{}
	}
	return c.Pix[y*c.Width+x]
}

// SampleBilinear reads the canvas at continuous pixel coordinates where
// (0,0) is the center of the top-left sample. Coordinates are clamped to the
// edge band so a placed source keeps its border color instead of fading into
// transparency; callers that need true outside semantics test the source
// bounds first. Premultiplied channels interpolate safely.
func (c *Canvas) SampleBilinear(x, y float64) paint.Pixel {
	x = math.Max(0.5, math.Min(float64(c.Width)-0.5, x))
	y = math.Max(0.5, math.Min(float64(c.Height)-0.5, y))
	fx, fy := x-0.5, y-0.5
	x0, y0 := int(math.Floor(fx)), int(math.Floor(fy))
	tx, ty := fx-float64(x0), fy-float64(y0)
	a := c.At(x0, y0)
	b := c.At(x0+1, y0)
	d := c.At(x0, y0+1)
	e := c.At(x0+1, y0+1)
	top := lerp(a, b, tx)
	bottom := lerp(d, e, tx)
	return lerp(top, bottom, ty)
}

func lerp(a, b paint.Pixel, t float64) paint.Pixel {
	return paint.Pixel{
		R: a.R + (b.R-a.R)*t,
		G: a.G + (b.G-a.G)*t,
		B: a.B + (b.B-a.B)*t,
		A: a.A + (b.A-a.A)*t,
	}
}

// RenderCanvas computes the canonical four-sample exposure without encoding it.
func RenderCanvas(ctx context.Context, c scene.Compiled) (*Canvas, error) {
	d := c.Document
	cv := &Canvas{Width: d.Width, Height: d.Height, Pix: make([]paint.Pixel, d.Width*d.Height)}
	for y := 0; y < d.Height; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := 0; x < d.Width; x++ {
			sum := paint.Pixel{}
			for sy := 0; sy < 2; sy++ {
				for sx := 0; sx < 2; sx++ {
					p := geom.Point{X: float64(x) + (float64(sx)+0.5)/2, Y: float64(y) + (float64(sy)+0.5)/2}
					dst := paint.From(d.Background)
					for _, l := range c.Layers {
						if len(l.Clip) > 0 && !l.Clip.Contains(p, "nonzero") {
							continue
						}
						if len(l.Mask) > 0 && !l.Mask.Contains(p, "nonzero") {
							continue
						}
						src := paint.Pixel{}
						for _, s := range l.Shapes {
							q := s.Inverse.Apply(p)
							if s.Path.Contains(q, s.Shape.FillRule) {
								src = paint.Over(src, paint.Sample(s.Shape.Brush, q), "normal")
							}
						}
						dst = paint.Over(dst, src.Scale(l.Layer.Opacity), l.Layer.Blend)
					}
					sum.R += dst.R / 4
					sum.G += dst.G / 4
					sum.B += dst.B / 4
					sum.A += dst.A / 4
				}
			}
			cv.Pix[y*d.Width+x] = sum
		}
	}
	return cv, nil
}

// EncodePNG converts linear samples to sRGB straight-alpha bytes.
func EncodePNG(cv *Canvas) ([]byte, error) {
	img := image.NewNRGBA(image.Rect(0, 0, cv.Width, cv.Height))
	for y := 0; y < cv.Height; y++ {
		for x := 0; x < cv.Width; x++ {
			r, g, b, a := cv.Pix[y*cv.Width+x].RGBA8()
			img.SetNRGBA(x, y, color.NRGBA{R: r, G: g, B: b, A: a})
		}
	}
	var out bytes.Buffer
	err := png.Encode(&out, img)
	return out.Bytes(), err
}

func Render(ctx context.Context, c scene.Compiled) ([]byte, error) {
	cv, err := RenderCanvas(ctx, c)
	if err != nil {
		return nil, err
	}
	return EncodePNG(cv)
}
