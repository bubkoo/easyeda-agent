package enclosure

import (
	"encoding/binary"
	"math"
	"strings"
	"testing"
)

func testSpec() *Spec {
	return &Spec{
		Version: 1, Unit: "mm", Resolution: 0.25,
		Outline: Outline{Mode: "rect", Clearance: 0.5},
		Wall:    2, Floor: 2, Lid: 2,
		Board:  Board{Outline: []Point{{0, 0}, {20, 0}, {20, 16}, {0, 16}}, Thickness: 1.6, Standoff: 3},
		Height: 15, Split: 6.6,
		Lip:        Lip{Height: 2, Gap: 0.2},
		Components: []Component{{Ref: "U1", Side: "top", BBox: [4]float64{5, 5, 10, 10}, Height: 3, HeightSource: "spec"}},
	}
}

func mustBuild(t *testing.T, s *Spec) *Result {
	t.Helper()
	r, err := Build(s)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBuildProducesTwoClosedShells(t *testing.T) {
	r := mustBuild(t, testSpec())
	if !r.OK {
		t.Fatalf("checks failed: %+v", r.Checks)
	}
	for _, p := range r.Parts {
		n := binary.LittleEndian.Uint32(p.STL[80:84])
		if int(n) != p.Triangles || len(p.STL) != 84+50*p.Triangles || p.Triangles == 0 {
			t.Fatalf("%s: bad STL framing (n=%d tris=%d len=%d)", p.Name, n, p.Triangles, len(p.STL))
		}
	}
	bottom, top := r.Parts[0], r.Parts[1]
	if bottom.BBox[2] != 0 || math.Abs(bottom.BBox[5]-8.6) > 1e-9 || math.Abs(top.BBox[2]-6.6) > 1e-9 || math.Abs(top.BBox[5]-15) > 1e-9 {
		t.Fatalf("z extents: bottom %v top %v", bottom.BBox, top.BBox)
	}
	// Outer footprint is the board bbox offset by clearance + wall = 2.5 mm.
	if math.Abs(bottom.BBox[0]+2.5) > 0.25 || math.Abs(bottom.BBox[3]-22.5) > 0.25 {
		t.Fatalf("xy extents: %v", bottom.BBox)
	}
	// Analytic bottom volume without lip and rounding: floor slab + wall ring.
	outer := func(o float64) float64 { return (20+2*o)*(16+2*o) - (4-math.Pi)*o*o }
	approx := outer(2.5)*2 + (outer(2.5)-outer(0.5))*4.6
	if math.Abs(bottom.Volume-approx)/approx > 0.12 {
		t.Fatalf("bottom volume %.1f far from analytic %.1f", bottom.Volume, approx)
	}
}

func TestComponentTallerThanCavityFails(t *testing.T) {
	s := testSpec()
	s.Components[0].Height = 20
	r := mustBuild(t, s)
	if r.OK || r.Parts[0].STL != nil || !strings.Contains(r.Checks[0].Detail, "U1") {
		t.Fatalf("expected component-clearance failure without STL: %+v", r.Checks)
	}
}

func TestSideCutoutClearsOverhangingConnector(t *testing.T) {
	s := testSpec()
	s.Components = append(s.Components, Component{Ref: "J1", Side: "top", BBox: [4]float64{-2, 6, 3, 10}, Height: 3, HeightSource: "spec"})
	if mustBuild(t, s).OK {
		t.Fatal("overhanging connector should hit the wall")
	}
	s.Features = []Feature{{ID: "J1-side", Op: "cut", Part: "both", Shape: "rect", X: -2, Y: 8, W: 6, H: 5, Z: [2]float64{6.6, 10}}}
	if r := mustBuild(t, s); !r.OK {
		t.Fatalf("cutout should clear J1: %+v", r.Checks)
	}
}

func TestPillarExpandsToPostsAndHoles(t *testing.T) {
	s := testSpec()
	s.Pillars = []Pillar{{ID: "H1", X: 15, Y: 12, Screw: "M2"}}
	r := mustBuild(t, s)
	if !r.OK {
		t.Fatalf("%+v", r.Checks)
	}
	g := s.newGrid()
	bottom, top := s.solids(g, "bottom"), s.solids(g, "top")
	at := func(solid []bool, x, y, z float64) bool {
		i, j := int((x-g.x0)/g.res), int((y-g.y0)/g.res)
		for k := 0; k < g.nz(); k++ {
			if z > g.z[k] && z < g.z[k+1] {
				return solid[g.idx(i, j, k)]
			}
		}
		t.Fatalf("z %.2f outside grid", z)
		return false
	}
	if at(bottom, 15.1, 12.1, 1) || at(bottom, 15.1, 12.1, 4) || !at(bottom, 17.1, 12.1, 4) {
		t.Fatal("bottom post must have a through clearance hole inside a solid post")
	}
	if at(top, 15.1, 12.1, 10) || !at(top, 17.1, 12.1, 10) || !at(top, 15.1, 12.1, 14) {
		t.Fatal("top post must have a blind pilot hole that stops at the lid")
	}
}

func TestAddFeatureProtrudesAndRotatedRectContains(t *testing.T) {
	s := testSpec()
	s.Features = []Feature{{ID: "boss", Op: "add", Part: "bottom", Shape: "rect", X: 25, Y: 8, W: 4, H: 2, Z: [2]float64{0, 4}}}
	r := mustBuild(t, s)
	if !r.OK || r.Parts[0].BBox[3] < 26.9 {
		t.Fatalf("boss should extend the bottom shell to x=27: %+v", r.Parts[0].BBox)
	}
	f := Feature{Shape: "rect", W: 4, H: 1, Rotation: 90}
	if !f.contains(0, 1.9) || f.contains(1.9, 0) {
		t.Fatal("rotated rect containment")
	}
}

func TestParseRejectsUnknownFieldsAndBadValues(t *testing.T) {
	if _, err := Parse([]byte(`{"version":1,"bogus":1}`)); err == nil {
		t.Fatal("unknown field accepted")
	}
	for _, mut := range []func(*Spec){
		func(s *Spec) { s.Wall = 0 },
		func(s *Spec) { s.Split = 14 },
		func(s *Spec) { s.Pillars = []Pillar{{ID: "H1", Screw: "M7"}} },
		func(s *Spec) {
			s.Features = []Feature{{ID: "f", Op: "drill", Part: "top", Shape: "rect", W: 1, H: 1, Z: [2]float64{0, 1}}}
		},
		func(s *Spec) { s.Board.Outline = s.Board.Outline[:2] },
	} {
		s := testSpec()
		mut(s)
		if s.Validate() == nil {
			t.Fatalf("invalid spec accepted: %+v", s)
		}
	}
}

func TestPlanDerivesPillarsOpeningsHeightsAndBuildsClean(t *testing.T) {
	in := PlanInput{Board: []Point{{0, 0}, {30, 0}, {30, 20}, {0, 20}}, Components: []PlanComponent{
		{Ref: "H1", Footprint: "HOLE-M2.5", Side: "top", BBox: [4]float64{1, 1, 5, 5}, AnchorX: 3, AnchorY: 3},
		{Ref: "J1", Footprint: "USB-C", Side: "top", BBox: [4]float64{-1.5, 6, 6, 14}, ModelName: "USB-C_L7.5-W8.9-H3.2"},
		{Ref: "U1", Side: "top", BBox: [4]float64{12, 6, 18, 12}, ModelName: "SOIC-8_L5.3-W5.3-H1.9-LS7.9-P1.27"},
		{Ref: "SW1", Side: "top", BBox: [4]float64{22, 12, 26, 16}, HeightAttr: "2.0mm"},
		{Ref: "SW2", Side: "top", BBox: [4]float64{22, 3, 26, 7}, HeightAttr: "2.0mm"},
		{Ref: "X1", Side: "top", BBox: [4]float64{12, 14, 15, 17}},
	}}
	o := DefaultPlanOptions()
	o.Resolution, o.TopCutouts = 0.2, []string{"SW1"}
	o.Heights = map[string]float64{"U1": 1.9}
	s, err := Plan(in, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Pillars) != 1 || s.Pillars[0].Screw != "M2.5" || len(s.Components) != 5 {
		t.Fatalf("pillars %+v components %d", s.Pillars, len(s.Components))
	}
	byRef := map[string]Component{}
	for _, c := range s.Components {
		byRef[c.Ref] = c
	}
	if byRef["U1"].HeightSource != "spec" || byRef["SW1"].HeightSource != "attribute" || byRef["X1"].HeightSource != "default" {
		t.Fatalf("height sources: %+v", byRef)
	}
	if math.Abs(s.Height-(2+3+1.6+3.2+1+2)) > 1e-9 || s.Split != s.BoardTop() {
		t.Fatalf("height %.3f split %.3f", s.Height, s.Split)
	}
	ids := map[string]Feature{}
	for _, f := range s.Features {
		ids[f.ID] = f
	}
	if ids["J1-side"].Source != "edge-connector:J1" || ids["SW1-opening"].Part != "top" || len(s.Features) != 2 {
		t.Fatalf("features %+v", s.Features)
	}
	if len(s.Candidates) != 1 || !strings.HasPrefix(s.Candidates[0], "SW2") || len(s.Warnings) != 1 || !strings.Contains(s.Warnings[0], "X1") {
		t.Fatalf("candidates %v warnings %v", s.Candidates, s.Warnings)
	}
	r := mustBuild(t, s)
	if !r.OK {
		t.Fatalf("planned enclosure should build clean: %+v", r.Checks)
	}
	if _, err := Plan(PlanInput{Board: in.Board, Components: in.Components}, PlanOptions{Mode: "board", Resolution: 0.2, Wall: 2, Floor: 2, Lid: 2, Thickness: 1.6, Standoff: 3, TopCutouts: []string{"SW9"}}); err == nil {
		t.Fatal("unknown --top-cutout accepted")
	}
}
