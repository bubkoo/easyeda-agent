// Package enclosure generates a printable two-part PCB enclosure (bottom and
// top shell) from an editable parameter file. Geometry is rasterized on an XY
// grid with exact Z layers, combined cell by cell, and written as closed STL
// meshes. Units are mm, XY follows the PCB (y-up), z = 0 is the outer bottom.
// Design: docs/pcb-enclosure-design.md.
package enclosure

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
)

type Point = [2]float64

type Spec struct {
	Version    int         `json:"version"`
	Unit       string      `json:"unit"`
	Resolution float64     `json:"resolution"`
	Source     *Source     `json:"source,omitempty"`
	Type       string      `json:"type,omitempty"` // split (default): bottom + top shell; slide: box + sliding lid
	Slide      *Slide      `json:"slide,omitempty"`
	Outline    Outline     `json:"outline"`
	Wall       float64     `json:"wall"`
	Floor      float64     `json:"floor"`
	Lid        float64     `json:"lid"`
	Board      Board       `json:"board"`
	Height     float64     `json:"height"`
	Split      float64     `json:"split"`
	Lip        Lip         `json:"lip"`
	Pillars    []Pillar    `json:"pillars"`
	Features   []Feature   `json:"features"`
	Components []Component `json:"components"`
	Candidates []string    `json:"candidates,omitempty"`
	Warnings   []string    `json:"warnings,omitempty"`
}

type Source struct {
	BoardSHA256  string `json:"boardSha256,omitempty"`
	Project      string `json:"project,omitempty"`
	DocumentUUID string `json:"documentUuid,omitempty"`
}

// Outline: mode "board" follows the board polygon, "rect" its bounding box.
// The cavity is everything within Clearance of that shape.
type Outline struct {
	Mode      string  `json:"mode"`
	Clearance float64 `json:"clearance"`
}

type Board struct {
	Outline   []Point `json:"outline"`
	Thickness float64 `json:"thickness"`
	Standoff  float64 `json:"standoff"`
}

type Lip struct {
	Height    float64 `json:"height"`
	Gap       float64 `json:"gap"`
	SnapDepth float64 `json:"snapDepth,omitempty"`
	Snaps     []Snap  `json:"snaps,omitempty"`
}

// Snap is a bump on the bottom lip's outer face that clicks into a matching
// window through the top wall. X/Y is a point on the wall; W is the bump width along the wall.
type Snap struct {
	ID string  `json:"id"`
	X  float64 `json:"x"`
	Y  float64 `json:"y"`
	W  float64 `json:"w"`
}

// Slide makes a push-cover box (rectangular outlines only): the walls carry a
// groove below the rim and the lid slides in through the open Side.
type Slide struct {
	Side   string  `json:"side"`   // x- | x+ | y- | y+
	Groove float64 `json:"groove"` // groove depth into the wall
	Gap    float64 `json:"gap"`    // play between lid and groove
	Retain float64 `json:"retain"` // rim height above the groove
}

type Pillar struct {
	ID     string  `json:"id"`
	X      float64 `json:"x"`
	Y      float64 `json:"y"`
	Screw  string  `json:"screw"`
	Source string  `json:"source,omitempty"`
}

// Feature is the single primitive behind cutouts, pockets and solid bosses: a
// planar shape swept over Z. All adds are applied before any cut.
type Feature struct {
	ID       string     `json:"id"`
	Op       string     `json:"op"`    // add | cut
	Part     string     `json:"part"`  // top | bottom | both
	Shape    string     `json:"shape"` // rect | circle
	X        float64    `json:"x"`
	Y        float64    `json:"y"`
	W        float64    `json:"w,omitempty"`
	H        float64    `json:"h,omitempty"`
	D        float64    `json:"d,omitempty"`
	Rotation float64    `json:"rotation,omitempty"`
	Z        [2]float64 `json:"z"`
	Source   string     `json:"source,omitempty"`
}

// Component is a keep-out box used only for interference checks.
type Component struct {
	Ref          string     `json:"ref"`
	Side         string     `json:"side"` // top | bottom
	BBox         [4]float64 `json:"bbox"` // minX, minY, maxX, maxY
	Height       float64    `json:"height"`
	HeightSource string     `json:"heightSource"`
}

// Screw sizes (mm): pillar outer diameter, top-shell pilot hole, bottom-shell
// clearance hole, counterbore diameter and depth.
type screwSize struct{ Outer, Pilot, Clear, HeadD, HeadDepth float64 }

var Screws = map[string]screwSize{
	"M2":   {5, 1.6, 2.4, 4.0, 2.0},
	"M2.5": {6, 2.1, 2.9, 5.0, 2.5},
	"M3":   {7, 2.5, 3.4, 6.0, 3.0},
	"M4":   {9, 3.3, 4.5, 8.0, 4.0},
	"M5":   {11, 4.2, 5.5, 9.5, 5.0},
	"M6":   {13, 5.0, 6.6, 11.0, 6.0},
}

// Parse decodes a spec strictly: unknown fields are an error.
func Parse(data []byte) (*Spec, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var s Spec
	if err := dec.Decode(&s); err != nil {
		return nil, fmt.Errorf("enclosure spec: %w", err)
	}
	return &s, s.Validate()
}

func (s *Spec) BoardBottom() float64 { return s.Floor + s.Board.Standoff }
func (s *Spec) slide() bool          { return s.Type == "slide" }

// grooveZ is the slide groove's z range; the lid occupies it minus Gap.
func (s *Spec) grooveZ() (float64, float64) {
	top := s.Height - s.Slide.Retain
	return top - s.Lid - 2*s.Slide.Gap, top
}

// CavityTop is the underside of whatever closes the box.
func (s *Spec) CavityTop() float64 {
	if s.slide() {
		z0, _ := s.grooveZ()
		return z0
	}
	return s.Height - s.Lid
}
func (s *Spec) BoardTop() float64 { return s.BoardBottom() + s.Board.Thickness }

func (s *Spec) Validate() error {
	fail := func(format string, a ...any) error { return fmt.Errorf("enclosure spec: "+format, a...) }
	if s.Version != 1 || s.Unit != "mm" {
		return fail("version must be 1 and unit mm")
	}
	if !(s.Resolution >= 0.02 && s.Resolution <= 1) {
		return fail("resolution must be within 0.02..1 mm")
	}
	if s.Outline.Mode != "board" && s.Outline.Mode != "rect" {
		return fail("outline.mode must be board or rect")
	}
	for name, v := range map[string]float64{"wall": s.Wall, "floor": s.Floor, "lid": s.Lid, "board.thickness": s.Board.Thickness, "height": s.Height} {
		if !(v > 0) || math.IsInf(v, 0) {
			return fail("%s must be > 0", name)
		}
	}
	for name, v := range map[string]float64{"outline.clearance": s.Outline.Clearance, "board.standoff": s.Board.Standoff, "lip.height": s.Lip.Height, "lip.gap": s.Lip.Gap} {
		if !(v >= 0) || math.IsInf(v, 0) {
			return fail("%s must be >= 0", name)
		}
	}
	if len(s.Board.Outline) < 3 || math.Abs(polygonArea(s.Board.Outline)) < 1e-6 {
		return fail("board.outline needs a polygon with area")
	}
	switch s.Type {
	case "", "split":
		if s.Slide != nil {
			return fail("slide parameters need type slide")
		}
		if !(s.Split > s.Floor && s.Split+s.Lip.Height+s.Lip.Gap < s.Height-s.Lid) {
			return fail("split must satisfy floor < split and split + lip.height + lip.gap < height - lid")
		}
		if s.Lip.Height > 0 && s.Lip.Gap >= s.Wall {
			return fail("lip.gap must be smaller than wall")
		}
		if len(s.Lip.Snaps) > 0 {
			if s.Lip.Height <= 0 || !(s.Lip.SnapDepth > 0) || s.Lip.SnapDepth >= (s.Wall-s.Lip.Gap)/2 {
				return fail("snaps need lip.height > 0 and 0 < lip.snapDepth < (wall - lip.gap) / 2")
			}
		}
	case "slide":
		sl := s.Slide
		if sl == nil || s.Outline.Mode != "rect" {
			return fail("type slide needs a slide object and outline.mode rect")
		}
		if sl.Side != "x-" && sl.Side != "x+" && sl.Side != "y-" && sl.Side != "y+" {
			return fail("slide.side must be x-, x+, y- or y+")
		}
		if !(sl.Groove > 0 && sl.Groove < s.Wall) || !(sl.Gap >= 0 && sl.Gap < sl.Groove) || !(sl.Retain > 0) {
			return fail("slide needs 0 < groove < wall, 0 <= gap < groove and retain > 0")
		}
		if s.Split != 0 || s.Lip.Height != 0 || len(s.Lip.Snaps) > 0 {
			return fail("type slide has no split, lip or snaps; set them to 0/empty")
		}
	default:
		return fail("type must be split or slide")
	}
	if s.BoardTop() >= s.CavityTop() {
		return fail("board top (%.3f) must be below the lid (%.3f)", s.BoardTop(), s.CavityTop())
	}
	ids := map[string]bool{}
	for _, p := range s.Pillars {
		sz, ok := Screws[p.Screw]
		if p.ID == "" || ids[p.ID] || !ok {
			return fail("pillar %q needs a unique id and screw M2|M2.5|M3|M4|M5|M6", p.ID)
		}
		if sz.HeadDepth >= s.BoardBottom() {
			return fail("pillar %s: %s counterbore depth %.1f must be below the board bottom %.1f", p.ID, p.Screw, sz.HeadDepth, s.BoardBottom())
		}
		ids[p.ID] = true
	}
	for _, sn := range s.Lip.Snaps {
		if sn.ID == "" || ids[sn.ID] || !(sn.W > 0) {
			return fail("snap %q needs a unique id and w > 0", sn.ID)
		}
		ids[sn.ID] = true
	}
	for _, f := range s.Features {
		if f.ID == "" || ids[f.ID] {
			return fail("feature %q needs a unique id", f.ID)
		}
		ids[f.ID] = true
		if (f.Op != "add" && f.Op != "cut") || (f.Part != "top" && f.Part != "bottom" && f.Part != "both") {
			return fail("feature %s: op must be add|cut and part top|bottom|both", f.ID)
		}
		switch f.Shape {
		case "rect":
			if !(f.W > 0 && f.H > 0) {
				return fail("feature %s: rect needs w and h > 0", f.ID)
			}
		case "circle":
			if !(f.D > 0) {
				return fail("feature %s: circle needs d > 0", f.ID)
			}
		default:
			return fail("feature %s: shape must be rect or circle", f.ID)
		}
		if !(f.Z[1] > f.Z[0]) {
			return fail("feature %s: z must be [z0, z1] with z1 > z0", f.ID)
		}
	}
	for _, c := range s.Components {
		if c.Ref == "" || (c.Side != "top" && c.Side != "bottom") || !(c.Height >= 0) || !(c.BBox[2] > c.BBox[0] && c.BBox[3] > c.BBox[1]) {
			return fail("component %q needs ref, side top|bottom, height >= 0 and a non-empty bbox", c.Ref)
		}
	}
	return nil
}

func polygonArea(p []Point) float64 {
	a := 0.0
	for i := range p {
		j := (i + 1) % len(p)
		a += p[i][0]*p[j][1] - p[j][0]*p[i][1]
	}
	return a / 2
}
