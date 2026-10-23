package enclosure

import (
	"bytes"
	"encoding/binary"
	"math"
)

type vec = [3]float64

// gridVertex addresses a raster corner: x index, y index, z level index.
type gridVertex [3]int

type meshData struct {
	pos   []vec
	tris  [][3]int
	faces []face // the merged planar polygons the triangles came from
}

// face is a planar polygon (counter-clockwise around normal) in pos indices.
type face struct {
	ring   []int
	normal vec
}

// quad is a merged rectangular face; corners run a→b→c→d around the face.
type quad struct {
	c      [4]gridVertex
	normal vec
}

// mesh emits every face between a solid and an empty cell, merging coplanar
// faces greedily into rectangles. Normals point from solid to empty. Corners of
// neighbouring rectangles that land on a rectangle's edge are inserted into it
// (no T-junctions), so each edge is shared by exactly two triangles.
func mesh(g *grid, solid []bool) *meshData {
	at := func(i, j, k int) bool {
		if i < 0 || j < 0 || k < 0 || i >= g.nx || j >= g.ny || k >= g.nz() {
			return false
		}
		return solid[g.idx(i, j, k)]
	}
	var quads []quad
	// sweep scans the planes along one axis; vtx maps (plane, u, v) to a corner.
	sweep := func(planes, nu, nv int, cell func(p, u, v int) (bool, bool), vtx func(p, u, v int) gridVertex, normal vec) {
		pos, neg := make([]bool, nu*nv), make([]bool, nu*nv)
		for p := 0; p <= planes; p++ {
			for v := range nv {
				for u := range nu {
					a, b := cell(p, u, v)
					pos[v*nu+u], neg[v*nu+u] = a && !b, !a && b
				}
			}
			for _, side := range []struct {
				mask []bool
				sign float64
			}{{pos, 1}, {neg, -1}} {
				greedy(side.mask, nu, nv, func(u0, v0, u1, v1 int) {
					quads = append(quads, quad{c: [4]gridVertex{vtx(p, u0, v0), vtx(p, u1, v0), vtx(p, u1, v1), vtx(p, u0, v1)},
						normal: vec{normal[0] * side.sign, normal[1] * side.sign, normal[2] * side.sign}})
				})
			}
		}
	}
	nz := g.nz()
	sweep(g.nx, g.ny, nz, func(p, u, v int) (bool, bool) { return at(p-1, u, v), at(p, u, v) },
		func(p, u, v int) gridVertex { return gridVertex{p, u, v} }, vec{1, 0, 0})
	sweep(g.ny, g.nx, nz, func(p, u, v int) (bool, bool) { return at(u, p-1, v), at(u, p, v) },
		func(p, u, v int) gridVertex { return gridVertex{u, p, v} }, vec{0, 1, 0})
	sweep(nz, g.nx, g.ny, func(p, u, v int) (bool, bool) { return at(u, v, p-1), at(u, v, p) },
		func(p, u, v int) gridVertex { return gridVertex{u, v, p} }, vec{0, 0, 1})

	m := &meshData{}
	ids := map[gridVertex]int{}
	for _, q := range quads {
		for _, c := range q.c {
			if _, ok := ids[c]; !ok {
				ids[c] = len(m.pos)
				m.pos = append(m.pos, vec{g.x0 + float64(c[0])*g.res, g.y0 + float64(c[1])*g.res, g.z[c[2]]})
			}
		}
	}
	for _, q := range quads {
		ring := []int{}
		for e := range 4 {
			a, b := q.c[e], q.c[(e+1)%4]
			ring = append(ring, ids[a])
			axis, step := 0, 1
			for d := range 3 {
				if a[d] != b[d] {
					axis = d
				}
			}
			if b[axis] < a[axis] {
				step = -1
			}
			for t := a[axis] + step; t != b[axis]; t += step {
				p := a
				p[axis] = t
				if id, ok := ids[p]; ok {
					ring = append(ring, id)
				}
			}
		}
		if dot(cross(sub(m.pos[ring[1]], m.pos[ring[0]]), sub(m.pos[ids[q.c[2]]], m.pos[ring[0]])), q.normal) < 0 {
			for i, j := 0, len(ring)-1; i < j; i, j = i+1, j-1 {
				ring[i], ring[j] = ring[j], ring[i]
			}
		}
		m.faces = append(m.faces, face{ring: ring, normal: q.normal})
		if len(ring) == 4 {
			m.tris = append(m.tris, [3]int{ring[0], ring[1], ring[2]}, [3]int{ring[0], ring[2], ring[3]})
			continue
		}
		center := vec{}
		for _, c := range q.c {
			p := m.pos[ids[c]]
			center = vec{center[0] + p[0]/4, center[1] + p[1]/4, center[2] + p[2]/4}
		}
		ci := len(m.pos)
		m.pos = append(m.pos, center)
		for i := range ring {
			m.tris = append(m.tris, [3]int{ci, ring[i], ring[(i+1)%len(ring)]})
		}
	}
	return m
}

// greedy covers the true cells of a nu×nv mask with maximal rectangles and
// reports each as corner indices [u0,u1)×[v0,v1). The mask is consumed.
func greedy(mask []bool, nu, nv int, emit func(u0, v0, u1, v1 int)) {
	for v := range nv {
		for u := range nu {
			if !mask[v*nu+u] {
				continue
			}
			w := 1
			for u+w < nu && mask[v*nu+u+w] {
				w++
			}
			h := 1
		grow:
			for v+h < nv {
				for x := u; x < u+w; x++ {
					if !mask[(v+h)*nu+x] {
						break grow
					}
				}
				h++
			}
			for y := v; y < v+h; y++ {
				for x := u; x < u+w; x++ {
					mask[y*nu+x] = false
				}
			}
			emit(u, v, u+w, v+h)
		}
	}
}

func sub(a, b vec) vec { return vec{a[0] - b[0], a[1] - b[1], a[2] - b[2]} }
func cross(a, b vec) vec {
	return vec{a[1]*b[2] - a[2]*b[1], a[2]*b[0] - a[0]*b[2], a[0]*b[1] - a[1]*b[0]}
}
func dot(a, b vec) float64 {
	return a[0]*b[0] + a[1]*b[1] + a[2]*b[2]
}

func (m *meshData) volume() float64 {
	v := 0.0
	for _, t := range m.tris {
		v += dot(m.pos[t[0]], cross(m.pos[t[1]], m.pos[t[2]]))
	}
	return v / 6
}

// badEdges counts directed edges that are not matched by exactly one opposite
// edge: zero means a closed, consistently oriented 2-manifold edge structure.
func (m *meshData) badEdges() int {
	count := map[[2]int]int{}
	for _, t := range m.tris {
		for e := range 3 {
			count[[2]int{t[e], t[(e+1)%3]}]++
		}
	}
	bad := 0
	for e, n := range count {
		if n != 1 || count[[2]int{e[1], e[0]}] != 1 {
			bad++
		}
	}
	return bad
}

func (m *meshData) bbox() [6]float64 {
	b := [6]float64{math.Inf(1), math.Inf(1), math.Inf(1), math.Inf(-1), math.Inf(-1), math.Inf(-1)}
	for _, p := range m.pos {
		for a := range 3 {
			b[a], b[a+3] = math.Min(b[a], p[a]), math.Max(b[a+3], p[a])
		}
	}
	return b
}

// stl encodes binary STL (little-endian float32, mm).
func (m *meshData) stl(header string) []byte {
	var buf bytes.Buffer
	h := make([]byte, 80)
	copy(h, header)
	buf.Write(h)
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(m.tris)))
	for _, t := range m.tris {
		a, b, c := m.pos[t[0]], m.pos[t[1]], m.pos[t[2]]
		n := cross(sub(b, a), sub(c, a))
		if l := math.Sqrt(dot(n, n)); l > 0 {
			n = vec{n[0] / l, n[1] / l, n[2] / l}
		}
		for _, p := range []vec{n, a, b, c} {
			for _, v := range p {
				_ = binary.Write(&buf, binary.LittleEndian, float32(v))
			}
		}
		buf.Write([]byte{0, 0})
	}
	return buf.Bytes()
}
