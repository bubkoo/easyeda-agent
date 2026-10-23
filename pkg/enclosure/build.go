package enclosure

import (
	"fmt"
	"math"
	"sort"
)

// grid is the XY raster (cell centers at x0+(i+0.5)*res) plus exact Z levels.
type grid struct {
	x0, y0, res float64
	nx, ny      int
	z           []float64
	sd          []float64 // signed distance of each XY cell to the outline shape
	inBoard     []bool
}

func (g *grid) nz() int             { return len(g.z) - 1 }
func (g *grid) idx(i, j, k int) int { return (k*g.ny+j)*g.nx + i }
func (g *grid) center(i, j int) (float64, float64) {
	return g.x0 + (float64(i)+0.5)*g.res, g.y0 + (float64(j)+0.5)*g.res
}
func (g *grid) zmid(k int) float64 { return (g.z[k] + g.z[k+1]) / 2 }

// cellRange returns the index span of cells whose centers may fall inside b.
func (g *grid) cellRange(b [4]float64) (i0, i1, j0, j1 int) {
	clamp := func(v, n int) int { return max(0, min(n, v)) }
	i0 = clamp(int(math.Floor((b[0]-g.x0)/g.res)), g.nx)
	i1 = clamp(int(math.Ceil((b[2]-g.x0)/g.res))+1, g.nx)
	j0 = clamp(int(math.Floor((b[1]-g.y0)/g.res)), g.ny)
	j1 = clamp(int(math.Ceil((b[3]-g.y0)/g.res))+1, g.ny)
	return
}

// Part is one printable shell.
type Part struct {
	Name      string     `json:"name"`
	Triangles int        `json:"triangles"`
	Volume    float64    `json:"volumeMm3"`
	BBox      [6]float64 `json:"bbox"` // minX, minY, minZ, maxX, maxY, maxZ
	STL       []byte     `json:"-"`
	mesh      *meshData
}

type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail,omitempty"`
}

type Result struct {
	OK       bool     `json:"ok"`
	Parts    []Part   `json:"parts"`
	Checks   []Check  `json:"checks"`
	Warnings []string `json:"warnings,omitempty"`
	Grid     struct {
		NX, NY, NZ int
		Resolution float64
	} `json:"grid"`
}

// shape is the outline the cavity and walls are offset from.
func (s *Spec) shape() []Point {
	if s.Outline.Mode == "rect" {
		b := bboxOf(s.Board.Outline)
		return []Point{{b[0], b[1]}, {b[2], b[1]}, {b[2], b[3]}, {b[0], b[3]}}
	}
	return s.Board.Outline
}

// pillarFeatures expands screw pillars into ordinary add/cut features. A split
// shell clamps the board between a bottom post (screw from below, counterbored)
// and a top post (blind pilot hole); a slide box has bottom posts with a blind
// pilot hole for a screw driven down through the board.
func (s *Spec) pillarFeatures() (adds, cuts []Feature) {
	zbb, zbt, roof := s.BoardBottom(), s.BoardTop(), s.Height-s.Lid
	for _, p := range s.Pillars {
		sz := Screws[p.Screw]
		c := func(id, op, part string, d, z0, z1 float64) Feature {
			return Feature{ID: p.ID + "-" + id, Op: op, Part: part, Shape: "circle", X: p.X, Y: p.Y, D: d, Z: [2]float64{z0, z1}}
		}
		adds = append(adds, c("post-bottom", "add", "bottom", sz.Outer, 0, zbb))
		if s.slide() {
			cuts = append(cuts, c("pilot", "cut", "bottom", sz.Pilot, s.Floor, zbb))
			continue
		}
		adds = append(adds, c("post-top", "add", "top", sz.Outer, zbt, roof))
		cuts = append(cuts, c("clear", "cut", "bottom", sz.Clear, 0, zbb), c("counterbore", "cut", "bottom", sz.HeadD, 0, sz.HeadDepth),
			c("pilot", "cut", "top", sz.Pilot, zbt, roof))
	}
	return
}

// snapZ is the bump's z range on the lip.
func (s *Spec) snapZ() (float64, float64) {
	return s.Split + s.Lip.Height*0.25, s.Split + s.Lip.Height*0.75
}

func (s *Spec) newGrid() *grid {
	shape := s.shape()
	reach := s.Outline.Clearance + s.Wall
	b := bboxOf(shape)
	b = [4]float64{b[0] - reach, b[1] - reach, b[2] + reach, b[3] + reach}
	levels := map[float64]bool{0: true, s.Floor: true, s.BoardBottom(): true, s.BoardTop(): true,
		s.Height - s.Lid: true, s.Height: true}
	if s.slide() {
		z0, z1 := s.grooveZ()
		for _, z := range []float64{z0, z1, z0 + s.Slide.Gap, z1 - s.Slide.Gap} {
			levels[z] = true
		}
	} else {
		levels[s.Split] = true
	}
	if s.Lip.Height > 0 {
		levels[s.Split+s.Lip.Height] = true
		levels[s.Split+s.Lip.Height+s.Lip.Gap] = true
	}
	if len(s.Lip.Snaps) > 0 {
		z0, z1 := s.snapZ()
		for _, z := range []float64{z0, z1, z0 - s.Lip.Gap, z1 + s.Lip.Gap} {
			levels[z] = true
		}
	}
	adds, cuts := s.pillarFeatures()
	for _, f := range append(append(append([]Feature{}, s.Features...), adds...), cuts...) {
		levels[f.Z[0]], levels[f.Z[1]] = true, true
		if f.Op == "add" {
			e := f.extent()
			b = [4]float64{math.Min(b[0], e[0]), math.Min(b[1], e[1]), math.Max(b[2], e[2]), math.Max(b[3], e[3])}
		}
	}
	for _, c := range s.Components {
		z0, z1 := s.componentZ(c)
		levels[z0], levels[z1] = true, true
	}
	g := &grid{res: s.Resolution}
	g.x0, g.y0 = b[0]-g.res, b[1]-g.res
	g.nx = int(math.Ceil((b[2]-g.x0)/g.res)) + 1
	g.ny = int(math.Ceil((b[3]-g.y0)/g.res)) + 1
	for z := range levels {
		g.z = append(g.z, z)
	}
	sort.Float64s(g.z)
	g.sd, g.inBoard = make([]float64, g.nx*g.ny), make([]bool, g.nx*g.ny)
	for j := range g.ny {
		for i := range g.nx {
			x, y := g.center(i, j)
			g.sd[j*g.nx+i] = signedDistance(shape, x, y)
			g.inBoard[j*g.nx+i] = insidePolygon(s.Board.Outline, x, y)
		}
	}
	return g
}

func (s *Spec) componentZ(c Component) (float64, float64) {
	if c.Side == "bottom" {
		return s.BoardBottom() - c.Height, s.BoardBottom()
	}
	return s.BoardTop(), s.BoardTop() + c.Height
}

// baseSolid is the bare shell of one part at a cell (sd is the cell's signed
// distance to the outline shape, x/y its center).
func (s *Spec) baseSolid(part string, sd, x, y, z float64) bool {
	if s.slide() {
		return s.slideSolid(part, sd, x, y, z)
	}
	c, w, lip := s.Outline.Clearance, s.Wall, s.Lip
	if sd > c+w {
		return false
	}
	inLip := lip.Height > 0 && z >= s.Split && z < s.Split+lip.Height
	if part == "bottom" {
		switch {
		case z < s.Floor:
			return true
		case z < s.Split:
			return sd > c
		case inLip:
			return sd > c && sd <= c+(w-lip.Gap)/2
		}
		return false
	}
	switch {
	case z < s.Split || z >= s.Height:
		return false
	case z >= s.Height-s.Lid:
		return true
	case lip.Height > 0 && z < s.Split+lip.Height+lip.Gap:
		return sd > c+(w+lip.Gap)/2
	}
	return sd > c
}

// slideGeometry: beyond reports whether (x, y) lies past the shape on the
// entry side; lateral whether it is within the groove width across that side.
// The entry starts at the shape edge, not the cavity edge, so the rounded
// outer corners are cut away whole and the side walls end at full thickness
// instead of tapering into slivers.
func (s *Spec) slideGeometry(x, y float64) (beyond bool, lateral func(reach float64) bool) {
	b, c := bboxOf(s.shape()), s.Outline.Clearance
	along := y
	lo, hi := b[1], b[3]
	switch s.Slide.Side {
	case "x+":
		beyond = x > b[2]
	case "x-":
		beyond = x < b[0]
	case "y+":
		beyond, along, lo, hi = y > b[3], x, b[0], b[2]
	default:
		beyond, along, lo, hi = y < b[1], x, b[0], b[2]
	}
	return beyond, func(reach float64) bool { return along >= lo-c-reach && along <= hi+c+reach }
}

// slideSolid: part "bottom" is the box, "top" the sliding lid.
func (s *Spec) slideSolid(part string, sd, x, y, z float64) bool {
	c, w, sl := s.Outline.Clearance, s.Wall, s.Slide
	if sd > c+w || z < 0 || z >= s.Height {
		return false
	}
	gz0, gz1 := s.grooveZ()
	beyond, lateral := s.slideGeometry(x, y)
	if part == "bottom" {
		if z < s.Floor {
			return true
		}
		inGroove := z >= gz0 && z < gz1 && sd <= c+sl.Groove
		return sd > c && !inGroove && !(beyond && z >= gz0)
	}
	if z < gz0+sl.Gap || z >= gz1-sl.Gap {
		return false
	}
	return sd <= c+sl.Groove-sl.Gap || (beyond && lateral(sl.Groove-sl.Gap))
}

// inPart reports whether an add feature at height z belongs to the part.
func (s *Spec) inPart(part string, z float64) bool {
	if s.slide() {
		if part == "bottom" {
			return true
		}
		z0, z1 := s.grooveZ()
		return z >= z0+s.Slide.Gap && z < z1-s.Slide.Gap
	}
	return (part == "bottom") == (z < s.Split)
}

// applySnaps adds lip bumps (bottom) or wall recesses (top) and returns the
// number of cells each snap changed.
func (s *Spec) applySnaps(g *grid, part string, solid []bool) map[string]int {
	counts := map[string]int{}
	c, w, gap, depth := s.Outline.Clearance, s.Wall, s.Lip.Gap, s.Lip.SnapDepth
	z0, z1 := s.snapZ()
	inner, outer, grow := c+(w-gap)/2, c+(w-gap)/2+depth, 0.0
	if part == "top" {
		// The recess is a window through the top wall: a blind pocket would
		// leave a sub-millimetre skin.
		inner, outer, grow = c+(w+gap)/2, c+w+1, gap
		z0, z1 = z0-gap, z1+gap
	}
	for _, sn := range s.Lip.Snaps {
		half := sn.W/2 + grow
		box := Feature{Shape: "rect", X: sn.X, Y: sn.Y, W: 2 * half, H: 2 * half}
		i0, i1, j0, j1 := g.cellRange(box.extent())
		for k := range g.nz() {
			if z := g.zmid(k); z < z0 || z > z1 {
				continue
			}
			for j := j0; j < j1; j++ {
				for i := i0; i < i1; i++ {
					x, y := g.center(i, j)
					if sd := g.sd[j*g.nx+i]; sd <= inner || sd > outer || !box.contains(x, y) {
						continue
					}
					if solid[g.idx(i, j, k)] != (part == "bottom") {
						solid[g.idx(i, j, k)] = part == "bottom"
						counts[sn.ID]++
					}
				}
			}
		}
	}
	return counts
}

func (s *Spec) solids(g *grid, part string) ([]bool, map[string]int) {
	solid := make([]bool, g.nx*g.ny*g.nz())
	for k := range g.nz() {
		z := g.zmid(k)
		for j := range g.ny {
			for i := range g.nx {
				x, y := g.center(i, j)
				solid[g.idx(i, j, k)] = s.baseSolid(part, g.sd[j*g.nx+i], x, y, z)
			}
		}
	}
	snaps := s.applySnaps(g, part, solid)
	apply := func(f Feature, add bool) {
		if f.Part != "both" && f.Part != part {
			return
		}
		i0, i1, j0, j1 := g.cellRange(f.extent())
		for k := range g.nz() {
			z := g.zmid(k)
			if z < f.Z[0] || z > f.Z[1] || (add && !s.inPart(part, z)) {
				continue
			}
			for j := j0; j < j1; j++ {
				for i := i0; i < i1; i++ {
					if x, y := g.center(i, j); f.contains(x, y) {
						solid[g.idx(i, j, k)] = add
					}
				}
			}
		}
	}
	padds, pcuts := s.pillarFeatures()
	for _, f := range append(append([]Feature{}, s.Features...), padds...) {
		if f.Op == "add" {
			apply(f, true)
		}
	}
	for _, f := range append(append([]Feature{}, s.Features...), pcuts...) {
		if f.Op == "cut" {
			apply(f, false)
		}
	}
	return solid, snaps
}

// MinFeature is the thinnest printable solid span: two 0.4 mm nozzle lines.
const MinFeature = 0.8

// thinRuns counts solid runs along X or Y that are bounded by empty cells on
// both sides and thinner than MinFeature, with one example location.
func thinRuns(g *grid, solid []bool) (int, [3]float64) {
	limit := int(math.Floor(MinFeature/g.res + 1e-9))
	n, at := 0, [3]float64{}
	scan := func(length int, cell func(t int) int, where func(t int) [3]float64) {
		run := 0
		for t := 0; t <= length; t++ {
			if t < length && solid[cell(t)] {
				run++
				continue
			}
			if run > 0 && run < limit && t < length && t-run > 0 {
				if n == 0 {
					at = where(t - run)
				}
				n++
			}
			run = 0
		}
	}
	for k := range g.nz() {
		for j := range g.ny {
			scan(g.nx, func(i int) int { return g.idx(i, j, k) }, func(i int) [3]float64 { x, y := g.center(i, j); return [3]float64{x, y, g.zmid(k)} })
		}
		for i := range g.nx {
			scan(g.ny, func(j int) int { return g.idx(i, j, k) }, func(j int) [3]float64 { x, y := g.center(i, j); return [3]float64{x, y, g.zmid(k)} })
		}
	}
	return n, at
}

// Build computes both shells and their checks. Parts carry STL only when every
// check passed.
func Build(s *Spec) (*Result, error) {
	if err := s.Validate(); err != nil {
		return nil, err
	}
	if !s.slide() && len(s.Pillars) > 0 && (s.Split < s.BoardBottom() || s.Split > s.BoardTop()) {
		return nil, fmt.Errorf("enclosure spec: with pillars, split must lie within the board thickness [%.3f, %.3f]", s.BoardBottom(), s.BoardTop())
	}
	g := s.newGrid()
	if cells := g.nx * g.ny * g.nz(); cells > 60_000_000 {
		return nil, fmt.Errorf("enclosure grid too large (%d cells); raise resolution", cells)
	}
	res := &Result{OK: true}
	res.Grid.NX, res.Grid.NY, res.Grid.NZ, res.Grid.Resolution = g.nx, g.ny, g.nz(), g.res
	for name, v := range map[string]float64{"wall": s.Wall, "floor": s.Floor, "lid": s.Lid} {
		if v < 1.5 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s %.2f mm is below the recommended 1.5 mm", name, v))
		}
	}
	sort.Strings(res.Warnings)
	fail := func(name, detail string) {
		res.OK = false
		res.Checks = append(res.Checks, Check{Name: name, Detail: detail})
	}
	solids := map[string][]bool{}
	snapCells := map[string]map[string]int{}
	for _, part := range []string{"bottom", "top"} {
		solids[part], snapCells[part] = s.solids(g, part)
	}
	if len(s.Lip.Snaps) > 0 {
		var missing []string
		for _, sn := range s.Lip.Snaps {
			if snapCells["bottom"][sn.ID] == 0 || snapCells["top"][sn.ID] == 0 {
				missing = append(missing, sn.ID)
			}
		}
		if len(missing) > 0 {
			fail("snaps", fmt.Sprintf("snaps not on the lip (no bump or recess formed): %v; place x/y on the wall", missing))
		} else {
			res.Checks = append(res.Checks, Check{Name: "snaps", OK: true, Detail: fmt.Sprintf("%d snaps formed on the lip", len(s.Lip.Snaps))})
		}
	}
	// Interference: component keep-out boxes and the board slab must be empty.
	hits := func(inside func(i, j int) bool, z0, z1 float64, b [4]float64) int {
		n := 0
		i0, i1, j0, j1 := g.cellRange(b)
		for _, solid := range solids {
			for k := 0; k < g.nz(); k++ {
				if z := g.zmid(k); z <= z0 || z >= z1 {
					continue
				}
				for j := j0; j < j1; j++ {
					for i := i0; i < i1; i++ {
						if solid[g.idx(i, j, k)] && inside(i, j) {
							n++
						}
					}
				}
			}
		}
		return n
	}
	clean := true
	for _, c := range s.Components {
		z0, z1 := s.componentZ(c)
		inBox := func(i, j int) bool {
			x, y := g.center(i, j)
			return x > c.BBox[0] && x < c.BBox[2] && y > c.BBox[1] && y < c.BBox[3]
		}
		if n := hits(inBox, z0, z1, c.BBox); n > 0 {
			clean = false
			fail("component-clearance", fmt.Sprintf("%s (%s side, height %.2f mm from %s) overlaps the shell in %d cells", c.Ref, c.Side, c.Height, c.HeightSource, n))
		}
	}
	if clean {
		res.Checks = append(res.Checks, Check{Name: "component-clearance", OK: true, Detail: fmt.Sprintf("%d components clear of the shell (half-cell tolerance %.3f mm)", len(s.Components), g.res/2)})
	}
	if n := hits(func(i, j int) bool { return g.inBoard[j*g.nx+i] }, s.BoardBottom(), s.BoardTop(), bboxOf(s.Board.Outline)); n > 0 {
		fail("board-clearance", fmt.Sprintf("the board slab overlaps the shell in %d cells", n))
	} else {
		res.Checks = append(res.Checks, Check{Name: "board-clearance", OK: true})
	}
	overlap := 0
	for i, b := range solids["bottom"] {
		if b && solids["top"][i] {
			overlap++
		}
	}
	if overlap > 0 {
		fail("parts-overlap", fmt.Sprintf("the two parts share %d cells; an add feature reaches into the other part", overlap))
	} else {
		res.Checks = append(res.Checks, Check{Name: "parts-overlap", OK: true})
	}
	if s.slide() {
		if n := slideBlocked(g, s.Slide.Side, solids["top"], solids["bottom"]); n > 0 {
			fail("slide-path", fmt.Sprintf("the lid hits the box in %d cells when sliding out through %s", n, s.Slide.Side))
		} else {
			res.Checks = append(res.Checks, Check{Name: "slide-path", OK: true, Detail: "lid slides out through " + s.Slide.Side + " without touching the box"})
		}
	}
	names := map[string]string{"bottom": "bottom", "top": "top"}
	if s.slide() {
		names = map[string]string{"bottom": "box", "top": "lid"}
	}
	for _, part := range []string{"bottom", "top"} {
		if n, at := thinRuns(g, solids[part]); n > 0 {
			res.Warnings = append(res.Warnings, fmt.Sprintf("%s: %d solid spans thinner than %.1f mm (e.g. near x=%.2f y=%.2f z=%.2f); they may not print", names[part], n, MinFeature, at[0], at[1], at[2]))
		}
		m := mesh(g, solids[part])
		p := Part{Name: names[part], Triangles: len(m.tris), Volume: m.volume(), BBox: m.bbox(), mesh: m}
		want := 0.0
		for k := 0; k < g.nz(); k++ {
			for j := 0; j < g.ny; j++ {
				for i := 0; i < g.nx; i++ {
					if solids[part][g.idx(i, j, k)] {
						want += g.res * g.res * (g.z[k+1] - g.z[k])
					}
				}
			}
		}
		if want == 0 {
			fail("mesh-"+names[part], "part is empty")
		} else if bad := m.badEdges(); bad > 0 {
			fail("mesh-"+names[part], fmt.Sprintf("%d non-manifold edges (cells touching only along an edge); adjust features or resolution", bad))
		} else if math.Abs(p.Volume-want)/want > 1e-6 {
			fail("mesh-"+names[part], fmt.Sprintf("mesh volume %.4f differs from raster volume %.4f", p.Volume, want))
		} else {
			res.Checks = append(res.Checks, Check{Name: "mesh-" + names[part], OK: true, Detail: fmt.Sprintf("closed 2-manifold mesh, volume %.1f mm3", p.Volume)})
		}
		if res.OK {
			p.STL = m.stl("easyeda-agent enclosure " + names[part])
		}
		res.Parts = append(res.Parts, p)
	}
	if !res.OK {
		for i := range res.Parts {
			res.Parts[i].STL = nil
		}
	}
	return res, nil
}

// slideBlocked counts box cells that lie ahead of any lid cell in the sliding
// direction along the same row: zero means the lid can slide straight out.
func slideBlocked(g *grid, side string, lid, box []bool) int {
	n := 0
	alongX := side == "x+" || side == "x-"
	outward := side == "x+" || side == "y+"
	rows, length := g.nx, g.ny
	if alongX {
		rows, length = g.ny, g.nx
	}
	for k := range g.nz() {
		for r := range rows {
			cell := func(t int) int {
				if alongX {
					return g.idx(t, r, k)
				}
				return g.idx(r, t, k)
			}
			// Walk from the far inside towards the exit: after the first lid
			// cell, any box cell is in the way.
			seen := false
			for step := range length {
				t := step
				if !outward {
					t = length - 1 - step
				}
				if lid[cell(t)] {
					seen = true
				}
				if seen && box[cell(t)] {
					n++
				}
			}
		}
	}
	return n
}
