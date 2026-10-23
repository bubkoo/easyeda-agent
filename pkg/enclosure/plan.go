package enclosure

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// PlanComponent is one placed part from a board readback, already in mm.
type PlanComponent struct {
	Ref        string
	Footprint  string
	Side       string // top | bottom
	BBox       [4]float64
	AnchorX    float64
	AnchorY    float64
	ModelName  string // bound 3D model name, e.g. R0402_L1.0-W0.5-H0.4
	HeightAttr string // otherProperty Height, e.g. "1.3mm"
	Pads       []PlanPad
}

// PlanPad is one pad of a component; HoleMM is 0 without a drill and
// HoleKnown false when the readback carried no drill information.
type PlanPad struct {
	Number    string
	X, Y      float64 // mm
	Net       string
	HoleMM    float64
	HoleKnown bool
}

// PadHoleMM converts the host's raw drill tuple (["ROUND", d, d] in 10 mil
// units) to a round-hole diameter in mm. padMinMil, when > 0, is the pad's
// smaller side in mil: a drill must be smaller than its pad, which is how the
// 10 mil unit was established (a 66.9 mil header pad reads 4.3308 = 1.1 mm).
// ok is false for slots, malformed tuples or an implausible size.
func PadHoleMM(raw any, padMinMil float64) (mm float64, ok bool) {
	t, isList := raw.([]any)
	if !isList || len(t) < 2 || t[0] != "ROUND" {
		return 0, false
	}
	d, isNum := t[1].(float64)
	if !isNum || !(d > 0) {
		return 0, false
	}
	mil := d * 10
	if padMinMil > 0 && mil >= padMinMil {
		return 0, false
	}
	return mil * 0.0254, true
}

type PlanInput struct {
	Board      []Point
	Components []PlanComponent
	Source     *Source
}

type PlanOptions struct {
	Mode          string
	Resolution    float64
	Wall          float64
	Floor         float64
	Lid           float64
	Thickness     float64
	Standoff      float64
	Clearance     float64
	TopClearance  float64
	DefaultHeight float64
	EdgeMargin    float64
	CutoutMargin  float64
	Screw         string
	TopCutouts    []string
	Heights       map[string]float64 // per-designator overrides (heightSource "spec")
	Type          string             // split | slide
	SlideSide     string             // x- | x+ | y- | y+
	Pillars       []Pillar           // added by hand, in board mm
	Snaps         string             // auto (split shells without pillars) | none
}

func DefaultPlanOptions() PlanOptions {
	return PlanOptions{Mode: "board", Resolution: 0.1, Wall: 2, Floor: 2, Lid: 2, Thickness: 1.6, Standoff: 3,
		Clearance: 0.5, TopClearance: 1, DefaultHeight: 3, EdgeMargin: 1, CutoutMargin: 0.5, Screw: "M3",
		Type: "split", SlideSide: "x+", Snaps: "auto"}
}

var (
	modelHeightRe = regexp.MustCompile(`(?i)-H(\d+(?:\.\d+)?)`)
	attrHeightRe  = regexp.MustCompile(`^\s*(\d+(?:\.\d+)?)\s*(mm)?\s*$`)
	screwRe       = regexp.MustCompile(`(?i)M(2\.5|2|3|4|5|6)(?:[^0-9.]|$)`)
	connectorRe   = regexp.MustCompile(`^(J|CN|USB|CON|P)\d+$`)
	userInputRe   = regexp.MustCompile(`^(SW|KEY|BTN|LED|D)\d+$`)
)

// componentHeight returns the height and where it came from.
func componentHeight(c PlanComponent, fallback float64) (float64, string) {
	if m := modelHeightRe.FindStringSubmatch(c.ModelName); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			return v, "model-name"
		}
	}
	if m := attrHeightRe.FindStringSubmatch(c.HeightAttr); m != nil {
		if v, err := strconv.ParseFloat(m[1], 64); err == nil {
			return v, "attribute"
		}
	}
	return fallback, "default"
}

var groundNetRe = regexp.MustCompile(`(?i)^$|GND|^PE$|EARTH|SHIELD|CHASSIS`)

// screwFor picks the largest screw that passes a round hole of d mm.
func screwFor(d float64) (string, bool) {
	best := ""
	for _, n := range []struct {
		name string
		size float64
	}{{"M2", 2}, {"M2.5", 2.5}, {"M3", 3}, {"M4", 4}, {"M5", 5}, {"M6", 6}} {
		if n.size <= d-0.1 {
			best = n.name
		}
	}
	return best, best != ""
}

// mountingHoles returns the screw pillars a component stands for. With drill
// data, a mounting hole is a round hole that passes at least an M2 screw on a
// part whose pads carry no signal net (empty or ground); a part with no drill
// data falls back to a HOLE/MOUNT footprint name at its anchor (size unknown).
// Designators are never used: boards name pin headers H1, H2…
func mountingHoles(c PlanComponent, haveDrills bool, fallbackScrew string) []Pillar {
	if haveDrills {
		for _, p := range c.Pads {
			if !groundNetRe.MatchString(p.Net) {
				return nil
			}
		}
		var out []Pillar
		for _, p := range c.Pads {
			screw, ok := screwFor(p.HoleMM)
			if !p.HoleKnown || !ok {
				continue
			}
			id := c.Ref
			if len(c.Pads) > 1 {
				id = c.Ref + "-" + p.Number
			}
			out = append(out, Pillar{ID: id, X: round3(p.X), Y: round3(p.Y), Screw: screw,
				Source: fmt.Sprintf("mounting-hole:%s (drill %.2f mm)", c.Ref, p.HoleMM)})
		}
		return out
	}
	fp := strings.ToUpper(c.Footprint)
	if !strings.Contains(fp, "HOLE") && !strings.Contains(fp, "MOUNT") {
		return nil
	}
	screw := fallbackScrew
	if m := screwRe.FindStringSubmatch(c.Footprint); m != nil {
		screw = "M" + m[1]
	}
	return []Pillar{{ID: c.Ref, X: c.AnchorX, Y: c.AnchorY, Screw: screw, Source: "footprint-name:" + c.Ref + " (drill unknown)"}}
}

// Plan derives an editable enclosure spec from a board readback. Every derived
// pillar and feature names its source so the result can be reviewed.
func Plan(in PlanInput, o PlanOptions) (*Spec, error) {
	if len(in.Board) < 3 {
		return nil, fmt.Errorf("enclosure plan: board outline needs at least 3 points")
	}
	top := map[string]bool{}
	for _, r := range o.TopCutouts {
		top[r] = true
	}
	if o.Type == "slide" && o.Mode != "rect" {
		return nil, fmt.Errorf("enclosure plan: a slide box needs --mode rect")
	}
	s := &Spec{Version: 1, Unit: "mm", Resolution: o.Resolution, Source: in.Source, Type: o.Type,
		Outline: Outline{Mode: o.Mode, Clearance: o.Clearance},
		Wall:    o.Wall, Floor: o.Floor, Lid: o.Lid,
		Board: Board{Outline: in.Board, Thickness: o.Thickness, Standoff: o.Standoff},
		Lip:   Lip{Height: 2, Gap: 0.2}, Pillars: []Pillar{}, Features: []Feature{}, Components: []Component{}}
	maxTop, maxBottom := 0.0, 0.0
	var defaulted []string
	seen := map[string]bool{}
	haveDrills := false
	for _, c := range in.Components {
		for _, p := range c.Pads {
			haveDrills = haveDrills || p.HoleKnown
		}
	}
	if !haveDrills {
		s.Warnings = append(s.Warnings, "board readback has no drill data (dump with a connector that reports pad holes); mounting holes guessed from HOLE/MOUNT footprint names only")
	}
	for _, c := range in.Components {
		seen[c.Ref] = true
		if holes := mountingHoles(c, haveDrills, o.Screw); len(holes) > 0 {
			s.Pillars = append(s.Pillars, holes...)
			continue
		}
		h, src := componentHeight(c, o.DefaultHeight)
		if v, ok := o.Heights[c.Ref]; ok {
			h, src = v, "spec"
		}
		if src == "default" {
			defaulted = append(defaulted, c.Ref)
		}
		if c.Side == "bottom" {
			maxBottom = math.Max(maxBottom, h)
		} else {
			maxTop = math.Max(maxTop, h)
		}
		s.Components = append(s.Components, Component{Ref: c.Ref, Side: c.Side, BBox: c.BBox, Height: h, HeightSource: src})
		if userInputRe.MatchString(c.Ref) && !top[c.Ref] {
			s.Candidates = append(s.Candidates, c.Ref+": possible top cutout (use --top-cutout)")
		}
	}
	for _, r := range o.TopCutouts {
		if !seen[r] {
			return nil, fmt.Errorf("enclosure plan: --top-cutout %s is not on the board", r)
		}
	}
	for r, v := range o.Heights {
		if !seen[r] || !(v >= 0) {
			return nil, fmt.Errorf("enclosure plan: --height %s=%v needs a designator on the board and a height >= 0", r, v)
		}
	}
	for _, p := range o.Pillars {
		if p.Source == "" {
			p.Source = "manual"
		}
		s.Pillars = append(s.Pillars, p)
	}
	if maxBottom > 0 {
		s.Board.Standoff = math.Max(s.Board.Standoff, maxBottom+o.TopClearance)
	}
	if o.Type == "slide" {
		s.Lip = Lip{}
		s.Slide = &Slide{Side: o.SlideSide, Groove: round3(o.Wall / 2), Gap: 0.2, Retain: 1}
		s.Height = round3(s.BoardTop() + maxTop + o.TopClearance + 2*s.Slide.Gap + o.Lid + s.Slide.Retain)
	} else {
		s.Split = s.BoardTop()
		s.Height = round3(s.BoardTop() + maxTop + o.TopClearance + o.Lid)
		if o.Snaps == "auto" && len(s.Pillars) == 0 {
			s.Lip.SnapDepth = 0.3
			s.Lip.Snaps = autoSnaps(s.shape(), o.Clearance+o.Wall/2)
		}
	}
	bb := bboxOf(in.Board)
	for _, c := range s.Components {
		z0, z1 := s.componentZ(c)
		if top[c.Ref] {
			b, m := c.BBox, o.CutoutMargin
			f := Feature{ID: c.Ref + "-opening", Op: "cut", Shape: "rect", X: round3((b[0] + b[2]) / 2), Y: round3((b[1] + b[3]) / 2),
				W: round3(b[2] - b[0] + 2*m), H: round3(b[3] - b[1] + 2*m), Source: "top-cutout:" + c.Ref}
			if c.Side == "bottom" {
				f.Part, f.Z = "bottom", [2]float64{0, s.Floor}
			} else {
				f.Part, f.Z = "top", [2]float64{round3(s.CavityTop()), s.Height}
			}
			s.Features = append(s.Features, f)
		}
		if f, ok := s.edgeOpening(c, bb, z0, z1, o); ok {
			s.Features = append(s.Features, f)
		}
	}
	s.Features = mergeCloseOpenings(s.Features)
	sort.Strings(defaulted)
	if len(defaulted) > 0 {
		s.Warnings = append(s.Warnings, fmt.Sprintf("height unknown, default %.1f mm used for: %s — set components[].height and heightSource \"spec\"", o.DefaultHeight, strings.Join(defaulted, ", ")))
	}
	if len(s.Pillars) == 0 {
		s.Warnings = append(s.Warnings, "no mounting holes or --pillar: the board rests loose on the floor")
	}
	return s, s.Validate()
}

// edgeOpening cuts the wall beside a connector that reaches the board edge.
func (s *Spec) edgeOpening(c Component, bb [4]float64, z0, z1 float64, o PlanOptions) (Feature, bool) {
	if !connectorRe.MatchString(c.Ref) {
		return Feature{}, false
	}
	b, m := c.BBox, o.CutoutMargin
	reach := s.Outline.Clearance + s.Wall + 1
	gaps := []float64{b[0] - bb[0], b[1] - bb[1], bb[2] - b[2], bb[3] - b[3]} // left, bottom, right, top
	edge := 0
	for i := range gaps {
		if gaps[i] < gaps[edge] {
			edge = i
		}
	}
	if gaps[edge] > o.EdgeMargin {
		return Feature{}, false
	}
	var x0, y0, x1, y1 float64
	switch edge {
	case 0:
		x0, x1, y0, y1 = math.Min(b[0], bb[0])-reach, bb[0], b[1]-m, b[3]+m
	case 1:
		x0, x1, y0, y1 = b[0]-m, b[2]+m, math.Min(b[1], bb[1])-reach, bb[1]
	case 2:
		x0, x1, y0, y1 = bb[2], math.Max(b[2], bb[2])+reach, b[1]-m, b[3]+m
	default:
		x0, x1, y0, y1 = b[0]-m, b[2]+m, bb[3], math.Max(b[3], bb[3])+reach
	}
	zlo, zhi := z0, z1+m
	if c.Side == "bottom" {
		zlo, zhi = z0-m, z1
	}
	return Feature{ID: c.Ref + "-side", Op: "cut", Part: "both", Shape: "rect",
		X: round3((x0 + x1) / 2), Y: round3((y0 + y1) / 2), W: round3(x1 - x0), H: round3(y1 - y0),
		Z: [2]float64{round3(zlo), round3(zhi)}, Source: "edge-connector:" + c.Ref}, true
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// autoSnaps puts one snap on the wall at the middle of each side of the shape's
// bounding box: the outermost crossing of the shape with that midline, pushed
// out by reach (clearance + half the wall).
func autoSnaps(shape []Point, reach float64) []Snap {
	b := bboxOf(shape)
	mx, my := (b[0]+b[2])/2, (b[1]+b[3])/2
	cross := func(horizontal bool, at float64) (lo, hi float64, ok bool) {
		lo, hi = math.Inf(1), math.Inf(-1)
		for i := range shape {
			p, q := shape[i], shape[(i+1)%len(shape)]
			a, c := p[1], q[1] // coordinate compared with at
			u, v := p[0], q[0] // coordinate reported
			if !horizontal {
				a, c, u, v = p[0], q[0], p[1], q[1]
			}
			if (a > at) == (c > at) || a == c {
				continue
			}
			x := u + (at-a)*(v-u)/(c-a)
			lo, hi, ok = math.Min(lo, x), math.Max(hi, x), true
		}
		return
	}
	var snaps []Snap
	if lo, hi, ok := cross(true, my); ok {
		w := math.Min(6, (b[3]-b[1])/4)
		snaps = append(snaps, Snap{ID: "snap-x-", X: round3(lo - reach), Y: round3(my), W: round3(w)},
			Snap{ID: "snap-x+", X: round3(hi + reach), Y: round3(my), W: round3(w)})
	}
	if lo, hi, ok := cross(false, mx); ok {
		w := math.Min(6, (b[2]-b[0])/4)
		snaps = append(snaps, Snap{ID: "snap-y-", X: round3(mx), Y: round3(lo - reach), W: round3(w)},
			Snap{ID: "snap-y+", X: round3(mx), Y: round3(hi + reach), W: round3(w)})
	}
	return snaps
}

// mergeCloseOpenings joins derived rectangular cuts on the same part and z
// range whose gap would leave a bar thinner than MinFeature: the bar cannot be
// printed, so one opening over both is the honest result.
func mergeCloseOpenings(fs []Feature) []Feature {
	rect := func(f Feature) [4]float64 { return [4]float64{f.X - f.W/2, f.Y - f.H/2, f.X + f.W/2, f.Y + f.H/2} }
	mergeable := func(f Feature) bool { return f.Op == "cut" && f.Shape == "rect" && f.Rotation == 0 }
	for merged := true; merged; {
		merged = false
	scan:
		for i := range fs {
			for j := i + 1; j < len(fs); j++ {
				a, b := fs[i], fs[j]
				if !mergeable(a) || !mergeable(b) || a.Part != b.Part || a.Z != b.Z {
					continue
				}
				ra, rb := rect(a), rect(b)
				dx := math.Max(0, math.Max(ra[0]-rb[2], rb[0]-ra[2]))
				dy := math.Max(0, math.Max(ra[1]-rb[3], rb[1]-ra[3]))
				if math.Hypot(dx, dy) >= MinFeature {
					continue
				}
				u := [4]float64{math.Min(ra[0], rb[0]), math.Min(ra[1], rb[1]), math.Max(ra[2], rb[2]), math.Max(ra[3], rb[3])}
				a.ID, a.Source = a.ID+"+"+b.ID, a.Source+"+"+b.Source
				a.X, a.Y, a.W, a.H = round3((u[0]+u[2])/2), round3((u[1]+u[3])/2), round3(u[2]-u[0]), round3(u[3]-u[1])
				fs[i] = a
				fs = append(fs[:j], fs[j+1:]...)
				merged = true
				break scan
			}
		}
	}
	return fs
}
