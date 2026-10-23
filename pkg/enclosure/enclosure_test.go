package enclosure

import (
	"encoding/binary"
	"math"
	"regexp"
	"strings"
	"testing"
)

var refPattern = regexp.MustCompile(`#[0-9]+`)

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
	bottom, _ := s.solids(g, "bottom")
	top, _ := s.solids(g, "top")
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
	if len(s.Candidates) != 1 || !strings.HasPrefix(s.Candidates[0], "SW2") || len(s.Warnings) != 2 || !strings.Contains(strings.Join(s.Warnings, ";"), "X1") {
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

func slideSpec() *Spec {
	s := testSpec()
	s.Type, s.Split, s.Lip = "slide", 0, Lip{}
	s.Slide = &Slide{Side: "x+", Groove: 1, Gap: 0.2, Retain: 1}
	return s
}

func TestSlideBoxLidSlidesOut(t *testing.T) {
	r := mustBuild(t, slideSpec())
	if !r.OK || r.Parts[0].Name != "box" || r.Parts[1].Name != "lid" || len(r.Warnings) != 0 {
		t.Fatalf("%+v %+v", r.Checks, r.Parts)
	}
	lid := r.Parts[1].BBox
	// Lid sits in the groove: z [11.8, 13.8], reaches the outer wall on x+ only.
	if math.Abs(lid[2]-11.8) > 1e-9 || math.Abs(lid[5]-13.8) > 1e-9 || lid[3] < 22.4 || lid[0] > -0.9 {
		t.Fatalf("lid bbox %v", lid)
	}
	var path bool
	for _, c := range r.Checks {
		path = path || (c.Name == "slide-path" && c.OK)
	}
	if !path {
		t.Fatalf("slide-path check missing: %+v", r.Checks)
	}
	// A boss on the box rim at the entry blocks the lid.
	s := slideSpec()
	s.Features = []Feature{{ID: "stop", Op: "add", Part: "bottom", Shape: "rect", X: 21.5, Y: 8, W: 1, H: 4, Z: [2]float64{11, 15}}}
	if r := mustBuild(t, s); r.OK {
		t.Fatal("blocked slide path accepted")
	}
}

func TestSlideRequiresRectAndNoSplit(t *testing.T) {
	for _, mut := range []func(*Spec){
		func(s *Spec) { s.Outline.Mode = "board" },
		func(s *Spec) { s.Split = 5 },
		func(s *Spec) { s.Slide.Side = "z+" },
		func(s *Spec) { s.Slide.Groove = 3 },
	} {
		s := slideSpec()
		mut(s)
		if s.Validate() == nil {
			t.Fatalf("invalid slide spec accepted: %+v", s.Slide)
		}
	}
}

func TestSnapsFormOnLipOrFail(t *testing.T) {
	s := testSpec()
	s.Lip.SnapDepth = 0.3
	s.Lip.Snaps = []Snap{{ID: "S1", X: -1.5, Y: 8, W: 4}, {ID: "S2", X: 21.5, Y: 8, W: 4}}
	r := mustBuild(t, s)
	if !r.OK {
		t.Fatalf("%+v", r.Checks)
	}
	s.Lip.Snaps = append(s.Lip.Snaps, Snap{ID: "S3", X: 10, Y: 8, W: 4})
	if r := mustBuild(t, s); r.OK {
		t.Fatal("snap away from the wall accepted")
	}
}

func TestThinSpanWarns(t *testing.T) {
	s := testSpec()
	s.Features = []Feature{
		{ID: "a", Op: "cut", Part: "top", Shape: "rect", X: 4, Y: 8, W: 4, H: 4, Z: [2]float64{13, 15}},
		{ID: "b", Op: "cut", Part: "top", Shape: "rect", X: 8.3, Y: 8, W: 4, H: 4, Z: [2]float64{13, 15}},
	}
	r := mustBuild(t, s)
	if !r.OK || len(r.Warnings) == 0 || !strings.Contains(strings.Join(r.Warnings, ";"), "thinner than 0.8") {
		t.Fatalf("expected thin-span warning: %v", r.Warnings)
	}
	if r := mustBuild(t, testSpec()); len(r.Warnings) != 0 {
		t.Fatalf("plain shell should not warn: %v", r.Warnings)
	}
}

func TestPreviewAndStepEncodeEveryFace(t *testing.T) {
	r := mustBuild(t, testSpec())
	img, err := Preview(r.Parts, 400, 300)
	if err != nil || len(img) < 1000 || string(img[1:4]) != "PNG" {
		t.Fatalf("preview: %v (%d bytes)", err, len(img))
	}
	for _, p := range r.Parts {
		data, err := p.STEP(p.Name)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if !strings.HasPrefix(text, "ISO-10303-21;") || !strings.HasSuffix(text, "END-ISO-10303-21;\n") {
			t.Fatal("STEP framing")
		}
		// Every #ref must resolve to a defined entity; faces = merged polygons.
		defined := map[string]bool{}
		for _, line := range strings.Split(text, "\n") {
			if i := strings.Index(line, "="); strings.HasPrefix(line, "#") && i > 0 {
				defined[line[:i]] = true
			}
		}
		for _, line := range strings.Split(text, "\n") {
			body := line
			if i := strings.Index(line, "="); strings.HasPrefix(line, "#") && i > 0 {
				body = line[i+1:]
			}
			for _, ref := range refPattern.FindAllString(body, -1) {
				if !defined[ref] {
					t.Fatalf("%s: dangling %s in %q", p.Name, ref, line)
				}
			}
		}
		if got := strings.Count(text, "FACE_SURFACE("); got != len(p.mesh.faces) || got == 0 {
			t.Fatalf("%s: %d FACE_SURFACE for %d faces", p.Name, got, len(p.mesh.faces))
		}
	}
}

func TestPlanSlideSnapsAndManualPillars(t *testing.T) {
	board := []Point{{0, 0}, {30, 0}, {30, 20}, {0, 20}}
	parts := []PlanComponent{{Ref: "U1", Side: "top", BBox: [4]float64{12, 6, 18, 12}, ModelName: "X-H1.9"},
		{Ref: "SW1", Side: "top", BBox: [4]float64{22, 12, 26, 16}, HeightAttr: "2mm"}}
	o := DefaultPlanOptions()
	o.Resolution = 0.2
	// Split shell without holes: four auto snaps that really form on the lip.
	s, err := Plan(PlanInput{Board: board, Components: parts}, o)
	if err != nil || len(s.Lip.Snaps) != 4 {
		t.Fatalf("auto snaps: %v %+v", err, s.Lip.Snaps)
	}
	if r := mustBuild(t, s); !r.OK || len(r.Warnings) != 0 {
		t.Fatalf("auto-snap shell: %+v %v", r.Checks, r.Warnings)
	}
	// Manual pillar suppresses auto snaps.
	o.Pillars = []Pillar{{ID: "P1", X: 4, Y: 4, Screw: "M2"}}
	s, err = Plan(PlanInput{Board: board, Components: parts}, o)
	if err != nil || len(s.Lip.Snaps) != 0 || s.Pillars[0].Source != "manual" {
		t.Fatalf("manual pillar: %v %+v", err, s)
	}
	if r := mustBuild(t, s); !r.OK {
		t.Fatalf("pillar shell: %+v", r.Checks)
	}
	// Slide box with a lid opening for SW1.
	o.Pillars, o.Type, o.TopCutouts = nil, "slide", []string{"SW1"}
	if _, err := Plan(PlanInput{Board: board, Components: parts}, o); err == nil {
		t.Fatal("slide with board outline accepted")
	}
	o.Mode = "rect"
	s, err = Plan(PlanInput{Board: board, Components: parts}, o)
	if err != nil {
		t.Fatal(err)
	}
	r := mustBuild(t, s)
	if !r.OK || r.Parts[1].Name != "lid" {
		t.Fatalf("slide plan: %+v", r.Checks)
	}
}

func TestPlanMergesOpeningsThatWouldLeaveAThinBar(t *testing.T) {
	board := []Point{{0, 0}, {30, 0}, {30, 20}, {0, 20}}
	parts := []PlanComponent{
		{Ref: "SW1", Side: "top", BBox: [4]float64{4, 8, 8, 12}, HeightAttr: "2mm"},
		{Ref: "SW2", Side: "top", BBox: [4]float64{9.2, 8, 13.2, 12}, HeightAttr: "2mm"}, // openings 0.2 mm apart
		{Ref: "SW3", Side: "top", BBox: [4]float64{20, 8, 24, 12}, HeightAttr: "2mm"},
	}
	o := DefaultPlanOptions()
	o.Resolution, o.TopCutouts = 0.2, []string{"SW1", "SW2", "SW3"}
	s, err := Plan(PlanInput{Board: board, Components: parts}, o)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Features) != 2 || s.Features[0].ID != "SW1-opening+SW2-opening" || math.Abs(s.Features[0].W-10.2) > 1e-9 {
		t.Fatalf("features %+v", s.Features)
	}
	if r := mustBuild(t, s); !r.OK || len(r.Warnings) != 0 {
		t.Fatalf("merged openings should build clean: %+v %v", r.Checks, r.Warnings)
	}
}

func TestPadHoleMMUsesTenMilUnitsAndRejectsImplausibleDrills(t *testing.T) {
	if mm, ok := PadHoleMM([]any{"ROUND", 4.3308, 4.3308}, 66.9); !ok || math.Abs(mm-1.1) > 1e-3 {
		t.Fatalf("header drill %.4f %v", mm, ok)
	}
	for _, raw := range []any{nil, []any{"SLOT", 4.0, 10.0}, []any{"ROUND", "x"}, []any{"ROUND", 7.0}} {
		if _, ok := PadHoleMM(raw, 66.9); ok { // 70 mil drill in a 66.9 mil pad is impossible
			t.Fatalf("accepted %v", raw)
		}
	}
}

func TestMountingHolesComeFromDrillsNotDesignators(t *testing.T) {
	drill := func(n, net string, x, mm float64) PlanPad {
		return PlanPad{Number: n, X: x, Y: 5, Net: net, HoleMM: mm, HoleKnown: true}
	}
	parts := []PlanComponent{
		{Ref: "H1", Side: "top", BBox: [4]float64{2, 4, 6, 6}, HeightAttr: "8.5mm", // 2-pin header named H1
			Pads: []PlanPad{drill("1", "VCC", 3, 1.0), drill("2", "GND", 5.54, 1.0)}},
		{Ref: "MH1", Side: "top", BBox: [4]float64{20, 3, 24, 7}, Pads: []PlanPad{drill("1", "", 22, 3.2)}},
		{Ref: "MH2", Side: "top", BBox: [4]float64{26, 13, 30, 17}, Pads: []PlanPad{drill("1", "GND", 28, 2.2)}},
		{Ref: "J9", Side: "top", BBox: [4]float64{8, 10, 18, 18}, HeightAttr: "5mm", // RJ45-like: pegs + signal pins
			Pads: []PlanPad{drill("P1", "", 9, 3.25), drill("1", "TX+", 12, 0.9)}},
	}
	o := DefaultPlanOptions()
	o.Resolution = 0.2
	s, err := Plan(PlanInput{Board: []Point{{0, 0}, {32, 0}, {32, 20}, {0, 20}}, Components: parts}, o)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, p := range s.Pillars {
		got[p.ID] = p.Screw
	}
	if len(got) != 2 || got["MH1"] != "M3" || got["MH2"] != "M2" {
		t.Fatalf("pillars %+v", s.Pillars)
	}
	for _, w := range s.Warnings {
		if strings.Contains(w, "drill data") {
			t.Fatalf("drill data was present: %v", s.Warnings)
		}
	}
}
