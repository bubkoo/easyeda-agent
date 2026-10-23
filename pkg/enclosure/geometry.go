package enclosure

import "math"

// insidePolygon is the even-odd point-in-polygon test.
func insidePolygon(p []Point, x, y float64) bool {
	in := false
	for i, j := 0, len(p)-1; i < len(p); j, i = i, i+1 {
		xi, yi, xj, yj := p[i][0], p[i][1], p[j][0], p[j][1]
		if (yi > y) != (yj > y) && x < (xj-xi)*(y-yi)/(yj-yi)+xi {
			in = !in
		}
	}
	return in
}

func segmentDistance(x, y, ax, ay, bx, by float64) float64 {
	dx, dy := bx-ax, by-ay
	t := 0.0
	if l := dx*dx + dy*dy; l > 0 {
		t = math.Max(0, math.Min(1, ((x-ax)*dx+(y-ay)*dy)/l))
	}
	return math.Hypot(x-(ax+t*dx), y-(ay+t*dy))
}

// signedDistance is negative inside the polygon.
func signedDistance(p []Point, x, y float64) float64 {
	d := math.Inf(1)
	for i := range p {
		j := (i + 1) % len(p)
		d = math.Min(d, segmentDistance(x, y, p[i][0], p[i][1], p[j][0], p[j][1]))
	}
	if insidePolygon(p, x, y) {
		return -d
	}
	return d
}

func bboxOf(p []Point) [4]float64 {
	b := [4]float64{math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1)}
	for _, q := range p {
		b[0], b[1] = math.Min(b[0], q[0]), math.Min(b[1], q[1])
		b[2], b[3] = math.Max(b[2], q[0]), math.Max(b[3], q[1])
	}
	return b
}

// contains reports whether (x, y) lies in the feature's planar shape.
func (f Feature) contains(x, y float64) bool {
	dx, dy := x-f.X, y-f.Y
	if f.Shape == "circle" {
		return dx*dx+dy*dy <= f.D*f.D/4
	}
	s, c := math.Sincos(-f.Rotation * math.Pi / 180)
	lx, ly := dx*c-dy*s, dx*s+dy*c
	return math.Abs(lx) <= f.W/2 && math.Abs(ly) <= f.H/2
}

// extent is the feature's planar bounding box.
func (f Feature) extent() [4]float64 {
	r := f.D / 2
	if f.Shape == "rect" {
		s, c := math.Sincos(f.Rotation * math.Pi / 180)
		rx := math.Abs(f.W/2*c) + math.Abs(f.H/2*s)
		ry := math.Abs(f.W/2*s) + math.Abs(f.H/2*c)
		return [4]float64{f.X - rx, f.Y - ry, f.X + rx, f.Y + ry}
	}
	return [4]float64{f.X - r, f.Y - r, f.X + r, f.Y + r}
}
