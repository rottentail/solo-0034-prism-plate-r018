// Package geom compiles bounded vector contours and affine coordinate mappings.
package geom

import "math"

type Point struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type Matrix [6]float64

var Identity = Matrix{1, 0, 0, 1, 0, 0}

func (m Matrix) Apply(p Point) Point {
	return Point{m[0]*p.X + m[2]*p.Y + m[4], m[1]*p.X + m[3]*p.Y + m[5]}
}
func (m Matrix) Inverse() (Matrix, bool) {
	d := m[0]*m[3] - m[1]*m[2]
	if math.Abs(d) < 1e-10 {
		return Matrix{}, false
	}
	return Matrix{m[3] / d, -m[1] / d, -m[2] / d, m[0] / d, (m[2]*m[5] - m[3]*m[4]) / d, (m[1]*m[4] - m[0]*m[5]) / d}, true
}

type Op struct {
	Kind   string  `json:"kind"`
	Points []Point `json:"points,omitempty"`
}
type Contour []Point
type Path []Contour

// Flatten uses a fixed subdivision contract shared by raster and SVG export.
func Flatten(ops []Op) Path {
	var out Path
	var ring Contour
	var at Point
	flush := func() {
		if len(ring) >= 3 {
			out = append(out, ring)
		}
		ring = nil
	}
	for _, o := range ops {
		switch o.Kind {
		case "M":
			flush()
			at = o.Points[0]
			ring = append(ring, at)
		case "L":
			at = o.Points[0]
			ring = append(ring, at)
		case "Q":
			start := at
			control, end := o.Points[0], o.Points[1]
			for i := 1; i <= 16; i++ {
				t := float64(i) / 16
				u := 1 - t
				ring = append(ring, Point{u*u*start.X + 2*u*t*control.X + t*t*end.X, u*u*start.Y + 2*u*t*control.Y + t*t*end.Y})
			}
			at = end
		case "C":
			start := at
			a, b, end := o.Points[0], o.Points[1], o.Points[2]
			for i := 1; i <= 24; i++ {
				t := float64(i) / 24
				u := 1 - t
				ring = append(ring, Point{u*u*u*start.X + 3*u*u*t*a.X + 3*u*t*t*b.X + t*t*t*end.X, u*u*u*start.Y + 3*u*u*t*a.Y + 3*u*t*t*b.Y + t*t*t*end.Y})
			}
			at = end
		case "Z":
			flush()
		}
	}
	flush()
	return out
}
func (path Path) Contains(p Point, rule string) bool {
	winding := 0
	crossings := 0
	for _, ring := range path {
		for i, a := range ring {
			b := ring[(i+1)%len(ring)]
			if (a.Y <= p.Y && b.Y > p.Y) || (b.Y <= p.Y && a.Y > p.Y) {
				x := a.X + (p.Y-a.Y)*(b.X-a.X)/(b.Y-a.Y)
				if x > p.X {
					crossings++
					if b.Y > a.Y {
						winding++
					} else {
						winding--
					}
				}
			}
		}
	}
	if rule == "evenodd" {
		return crossings%2 == 1
	}
	return winding != 0
}

// Construction helpers keep placement composition explicit and testable.
// The coordinate system is y-down, so a positive angle rotates clockwise.
func Translate(x, y float64) Matrix   { return Matrix{1, 0, 0, 1, x, y} }
func ScaleMatrix(x, y float64) Matrix { return Matrix{x, 0, 0, y, 0, 0} }
func Rotate(degrees float64) Matrix {
	r := degrees * math.Pi / 180
	c, s := math.Cos(r), math.Sin(r)
	return Matrix{c, s, -s, c, 0, 0}
}

// Multiply returns a∘b: b is applied first, then a.
func Multiply(a, b Matrix) Matrix {
	return Matrix{
		a[0]*b[0] + a[2]*b[1],
		a[1]*b[0] + a[3]*b[1],
		a[0]*b[2] + a[2]*b[3],
		a[1]*b[2] + a[3]*b[3],
		a[0]*b[4] + a[2]*b[5] + a[4],
		a[1]*b[4] + a[3]*b[5] + a[5],
	}
}
