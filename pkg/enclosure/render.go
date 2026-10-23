package enclosure

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"math"
)

// Preview renders the parts side by side as a shaded isometric PNG: the first
// part as printed, the second flipped over so its inside faces the viewer.
func Preview(parts []Part, width, height int) ([]byte, error) {
	type tri struct {
		p   [3]vec
		col [3]float64
	}
	colors := [][3]float64{{90, 150, 220}, {230, 150, 80}}
	var tris []tri
	offset := 0.0
	for n, p := range parts {
		if p.mesh == nil {
			continue
		}
		b := p.BBox
		cx, cy, cz := (b[0]+b[3])/2, (b[1]+b[4])/2, (b[2]+b[5])/2
		place := func(v vec) vec {
			x, y, z := v[0]-cx, v[1]-cy, v[2]-cz
			if n == 1 { // flip about X so the inside faces up
				y, z = -y, -z
			}
			return vec{x + offset, y, z + (b[5]-b[2])/2}
		}
		for _, t := range p.mesh.tris {
			tris = append(tris, tri{p: [3]vec{place(p.mesh.pos[t[0]]), place(p.mesh.pos[t[1]]), place(p.mesh.pos[t[2]])}, col: colors[n%2]})
		}
		offset += (b[3] - b[0]) * 1.25
	}
	az, el := -50*math.Pi/180, 35*math.Pi/180
	sa, ca := math.Sincos(az)
	se, ce := math.Sincos(el)
	proj := func(v vec) vec { // returns screen x, depth (towards viewer), screen y (up)
		x, y, z := v[0]*ca-v[1]*sa, v[0]*sa+v[1]*ca, v[2]
		return vec{x, y*ce - z*se, y*se + z*ce}
	}
	lo, hi := vec{math.Inf(1), math.Inf(1), math.Inf(1)}, vec{math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	for i := range tris {
		for j := range 3 {
			q := proj(tris[i].p[j])
			for a := range 3 {
				lo[a], hi[a] = math.Min(lo[a], q[a]), math.Max(hi[a], q[a])
			}
		}
	}
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for i := range img.Pix {
		img.Pix[i] = 255
	}
	if len(tris) == 0 {
		return encodePNG(img)
	}
	margin := 20.0
	scale := math.Min((float64(width)-2*margin)/(hi[0]-lo[0]), (float64(height)-2*margin)/(hi[2]-lo[2]))
	depth := make([]float64, width*height)
	for i := range depth {
		depth[i] = math.Inf(1)
	}
	light := vec{0.4, -0.5, 0.8}
	ll := math.Sqrt(dot(light, light))
	for _, t := range tris {
		n := cross(sub(t.p[1], t.p[0]), sub(t.p[2], t.p[0]))
		nl := math.Sqrt(dot(n, n))
		if nl == 0 {
			continue
		}
		shade := 0.35 + 0.65*math.Max(0, dot(n, light)/(nl*ll))
		var sx, sy, sz [3]float64
		for j := range 3 {
			q := proj(t.p[j])
			sx[j] = (q[0]-lo[0])*scale + margin
			sy[j] = float64(height) - ((q[2]-lo[2])*scale + margin)
			sz[j] = q[1]
		}
		d := (sy[1]-sy[2])*(sx[0]-sx[2]) + (sx[2]-sx[1])*(sy[0]-sy[2])
		if math.Abs(d) < 1e-12 {
			continue
		}
		x0, x1 := int(math.Max(0, math.Floor(min(sx[0], sx[1], sx[2])))), int(math.Min(float64(width-1), math.Ceil(max(sx[0], sx[1], sx[2]))))
		y0, y1 := int(math.Max(0, math.Floor(min(sy[0], sy[1], sy[2])))), int(math.Min(float64(height-1), math.Ceil(max(sy[0], sy[1], sy[2]))))
		c := color.RGBA{uint8(t.col[0] * shade), uint8(t.col[1] * shade), uint8(t.col[2] * shade), 255}
		for py := y0; py <= y1; py++ {
			for px := x0; px <= x1; px++ {
				X, Y := float64(px)+0.5, float64(py)+0.5
				w0 := ((sy[1]-sy[2])*(X-sx[2]) + (sx[2]-sx[1])*(Y-sy[2])) / d
				w1 := ((sy[2]-sy[0])*(X-sx[2]) + (sx[0]-sx[2])*(Y-sy[2])) / d
				w2 := 1 - w0 - w1
				if w0 < 0 || w1 < 0 || w2 < 0 {
					continue
				}
				if z := w0*sz[0] + w1*sz[1] + w2*sz[2]; z < depth[py*width+px] {
					depth[py*width+px] = z
					img.SetRGBA(px, py, c)
				}
			}
		}
	}
	return encodePNG(img)
}

func encodePNG(img image.Image) ([]byte, error) {
	var buf bytes.Buffer
	err := png.Encode(&buf, img)
	return buf.Bytes(), err
}
