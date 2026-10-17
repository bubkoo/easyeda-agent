package app

// cmd_pcb_silk_plan.go — offline designator placement (no editor writes). The
// connector's silk-align only tries upright text and applies all-or-nothing, so
// at exam-sized text (Arial ≥ 45 mil) a dense 0402 cluster leaves it unresolved.
// This plans every designator from a fresh dump with the real text bbox, allows
// a second orientation (90°, the exam's "≤ 2 directions"), keeps each label off
// pads, component bodies and other labels, and closer to its own part than to
// any other, then emits an `easyeda apply` playbook of typed pcb.silk.set writes.
// Measured on Web 4.1.78: the stored X/Y is the bbox's bottom-left anchor; at
// 90° the box is [X−h, X] × [Y, Y+w].

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

type spLabel struct {
	ID, Ref  string
	Comp     pcbRect
	W, H     float64 // upright text extent
	Fixed    bool
	Rotation float64
	X, Y     float64
	// New text (no primitive yet): Text is created by pcb.silk.add.
	Text     string
	Siblings []pcbRect // hint: the owner's other pads — the label must sit nearest its own
	Note     bool      // free note: no ownership; Comp is a 1×1 target point
	Box      pcbRect   // fixed label: its rendered box from the dump
	Free     bool      // a free string (hint/note), new or existing: top-left anchor, upright only
}

type spPlaced struct {
	Text     string  `json:"text,omitempty"`
	ID       string  `json:"primitiveId,omitempty"`
	Ref      string  `json:"designator"`
	X        float64 `json:"x"`
	Y        float64 `json:"y"`
	Rotation float64 `json:"rotation"`
	Box      pcbRect `json:"bbox"`
	Distance float64 `json:"distance"`
}

type spOptions struct {
	PadGap, BodyGap, LabelGap, EdgeGap, MaxDist, Step, RotPenalty float64
	Rotations                                                     []float64
}

func spBox(x, y, w, h, rot float64) pcbRect {
	if rot == 90 {
		return pcbRect{MinX: x - h, MinY: y, MaxX: x, MaxY: y + w}
	}
	return pcbRect{MinX: x, MinY: y, MaxX: x + w, MaxY: y + h}
}

// spAnchor is where a text's stored X/Y sits on its rendered box (measured, Web
// 4.1.78): a component attribute anchors bottom-left (at 90°: the box's
// bottom-right), a free string — what pcb.silk.add creates — anchors top-left.
// Free strings are only planned upright.
func spAnchor(b pcbRect, rot float64, freeString bool) (float64, float64) {
	switch {
	case freeString:
		return b.MinX, b.MaxY
	case rot == 90:
		return b.MaxX, b.MinY
	default:
		return b.MinX, b.MinY
	}
}

func spOverlap(a, b pcbRect, gap float64) bool {
	return a.MinX < b.MaxX+gap && b.MinX < a.MaxX+gap && a.MinY < b.MaxY+gap && b.MinY < a.MaxY+gap
}

func spRectDist(a, b pcbRect) float64 {
	dx := math.Max(0, math.Max(a.MinX-b.MaxX, b.MinX-a.MaxX))
	dy := math.Max(0, math.Max(a.MinY-b.MaxY, b.MinY-a.MaxY))
	return math.Hypot(dx, dy)
}

// planSilk places every non-fixed label; it returns the placements and the
// designators with no legal slot. Labels are placed most-constrained first.
func planSilk(labels []spLabel, pads []pcbRect, bodies map[string]pcbRect, board pcbRect, o spOptions) ([]spPlaced, []string) {
	var placed []pcbRect
	for _, l := range labels {
		if l.Fixed {
			placed = append(placed, l.Box)
		}
	}
	legal := func(l spLabel, b pcbRect) bool {
		if b.MinX < board.MinX+o.EdgeGap || b.MaxX > board.MaxX-o.EdgeGap || b.MinY < board.MinY+o.EdgeGap || b.MaxY > board.MaxY-o.EdgeGap {
			return false
		}
		for _, p := range pads {
			if spOverlap(b, p, o.PadGap) {
				return false
			}
		}
		for _, body := range bodies {
			if spOverlap(b, body, o.BodyGap) {
				return false
			}
		}
		for _, q := range placed {
			if spOverlap(b, q, o.LabelGap) {
				return false
			}
		}
		// Ownership by the label's centre: a reader pairs a label with the part
		// nearest to where it sits. Edge-to-edge distance refused a label that
		// spans its part's full height next to stacked neighbours (C6 between
		// R1 and R2, live).
		if l.Note {
			return true
		}
		cx, cy := (b.MinX+b.MaxX)/2, (b.MinY+b.MaxY)/2
		own := rectPtDist(l.Comp.MinX, l.Comp.MinY, l.Comp.MaxX, l.Comp.MaxY, cx, cy)
		for ref, body := range bodies {
			if ref != l.Ref && rectPtDist(body.MinX, body.MinY, body.MaxX, body.MaxY, cx, cy) <= own {
				return false
			}
		}
		for _, sib := range l.Siblings {
			if rectPtDist(sib.MinX, sib.MinY, sib.MaxX, sib.MaxY, cx, cy) <= own {
				return false
			}
		}
		return true
	}
	// candidates around the part: each side, sliding along it, ring by ring.
	cands := func(l spLabel) []spPlaced {
		var out []spPlaced
		c := l.Comp
		if l.Note {
			tx, ty := (c.MinX+c.MaxX)/2, (c.MinY+c.MaxY)/2
			for gx := board.MinX; gx <= board.MaxX; gx += o.Step * 2 {
				for gy := board.MinY; gy <= board.MaxY; gy += o.Step * 2 {
					b := pcbRect{MinX: gx, MinY: gy, MaxX: gx + l.W, MaxY: gy + l.H}
					d := math.Hypot((b.MinX+b.MaxX)/2-tx, (b.MinY+b.MaxY)/2-ty)
					x, y := spAnchor(b, 0, true)
					out = append(out, spPlaced{ID: l.ID, Ref: l.Ref, Text: l.Text, X: round2(x), Y: round2(y), Box: b, Distance: round2(d)})
				}
			}
			sort.SliceStable(out, func(i, j int) bool { return out[i].Distance < out[j].Distance })
			return out
		}
		for _, rot := range o.Rotations {
			if l.Free && rot != 0 {
				continue
			}
			w, h := l.W, l.H
			if rot == 90 {
				w, h = l.H, l.W
			}
			for d := 0.0; d <= o.MaxDist; d += o.Step {
				var boxes []pcbRect
				for s := c.MinX - w; s <= c.MaxX; s += o.Step / 2 { // above / below
					boxes = append(boxes, pcbRect{MinX: s, MinY: c.MaxY + o.BodyGap + d, MaxX: s + w, MaxY: c.MaxY + o.BodyGap + d + h})
					boxes = append(boxes, pcbRect{MinX: s, MinY: c.MinY - o.BodyGap - d - h, MaxX: s + w, MaxY: c.MinY - o.BodyGap - d})
				}
				for s := c.MinY - h; s <= c.MaxY; s += o.Step / 2 { // right / left
					boxes = append(boxes, pcbRect{MinX: c.MaxX + o.BodyGap + d, MinY: s, MaxX: c.MaxX + o.BodyGap + d + w, MaxY: s + h})
					boxes = append(boxes, pcbRect{MinX: c.MinX - o.BodyGap - d - w, MinY: s, MaxX: c.MinX - o.BodyGap - d, MaxY: s + h})
				}
				for _, b := range boxes {
					x, y := spAnchor(b, rot, l.Free)
					cx, cy := (b.MinX+b.MaxX)/2, (b.MinY+b.MaxY)/2
					dist := spRectDist(b, c) + 0.05*math.Hypot(cx-(c.MinX+c.MaxX)/2, cy-(c.MinY+c.MaxY)/2)
					if rot == 90 {
						dist += o.RotPenalty
					}
					out = append(out, spPlaced{ID: l.ID, Ref: l.Ref, Text: l.Text, X: round2(x), Y: round2(y), Rotation: rot, Box: b, Distance: round2(dist)})
				}
			}
		}
		sort.SliceStable(out, func(i, j int) bool { return out[i].Distance < out[j].Distance })
		return out
	}
	pending := []spLabel{}
	for _, l := range labels {
		if !l.Fixed {
			pending = append(pending, l)
		}
	}
	var result []spPlaced
	var failed []string
	for len(pending) > 0 {
		// most constrained first: fewest legal candidates against current state
		best, bestN := -1, math.MaxInt
		var bestCands []spPlaced
		for i, l := range pending {
			var ok []spPlaced
			for _, c := range cands(l) {
				if legal(l, c.Box) {
					ok = append(ok, c)
				}
			}
			if len(ok) < bestN {
				best, bestN, bestCands = i, len(ok), ok
			}
		}
		l := pending[best]
		pending = append(pending[:best], pending[best+1:]...)
		if len(bestCands) == 0 {
			failed = append(failed, l.Ref)
			continue
		}
		result = append(result, bestCands[0])
		placed = append(placed, bestCands[0].Box)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Ref < result[j].Ref })
	sort.Strings(failed)
	return result, failed
}

func newPcbSilkPlanCmd(stdout io.Writer) *cobra.Command {
	var board, out, project, doc string
	var only, hints, notes []string
	o := spOptions{}
	var allow90 bool
	var fontFamily string
	c := &cobra.Command{
		Use:   "silk-plan",
		Short: "Plan designator positions offline with real text extents and up to two orientations (emits an apply playbook)",
		Long: `Plan the TOP designators of a fresh 'pcb dump' (set the font first, e.g.
'pcb silk-set --font-family Arial --font-size 45', so the dump carries the real
text extent). Each label stays off every pad (--pad-gap), every component body
(--body-gap), every other label (--label-gap) and the board edge, and sits closer
to its own part than to any other. Upright is preferred; --allow-90 adds the 90°
orientation (exam: characters in no more than two directions) at --rot-penalty.
--hint PAD=text adds an interface hint (pin name) next to a pad, nearer to it than
to the part's other pads; --note text@x,y adds free text (board name, author) at
the legal spot nearest the point. New text takes the designators' measured size
and is created with pcb.silk.add.
Labels are placed most-constrained first; designators without a legal slot are
listed, never forced. --only limits the plan to some designators (the rest stay
as obstacles where they are). Writes <out> and <out>.playbook.json of typed
pcb.silk.set steps; apply, then save, reload and check (pcb check silk rules,
pcb layout-lint) on fresh geometry.`,
		Args:    cobra.NoArgs,
		Example: `  easyeda pcb silk-plan --board board.json --allow-90 --out silk.json --apply-project ceshi --apply-doc PCB1`,
		RunE: func(cmd *cobra.Command, args []string) error {
			b, err := loadBoardSnapshotPath(board)
			if err != nil {
				return err
			}
			if b.Outline == nil {
				return fmt.Errorf("board dump has no outline")
			}
			bodies := map[string]pcbRect{}
			compRef := map[string]string{}
			var pads []pcbRect
			for _, cp := range b.Components {
				compRef[cp.ID] = cp.Designator
				if cp.BBox != nil && cp.Layer != 2 {
					bodies[cp.Designator] = pcbRect{MinX: cp.BBox.MinX, MinY: cp.BBox.MinY, MaxX: cp.BBox.MaxX, MaxY: cp.BBox.MaxY}
				}
				for _, p := range cp.Pads {
					if p.W > 0 && p.H > 0 && (p.Layer == 1 || p.Layer == 12) {
						pads = append(pads, pcbRect{MinX: p.X - p.W/2, MinY: p.Y - p.H/2, MaxX: p.X + p.W/2, MaxY: p.Y + p.H/2})
					}
				}
			}
			want := map[string]bool{}
			for _, r := range only {
				want[strings.ToUpper(r)] = true
			}
			var labels []spLabel
			for _, t := range b.Silk {
				if t.Layer != 3 || t.BBox == nil || (t.Visible != nil && !*t.Visible) {
					continue
				}
				ref := compRef[t.CompID]
				w, h := t.BBox.MaxX-t.BBox.MinX, t.BBox.MaxY-t.BBox.MinY
				rot := math.Mod(math.Round(t.Rotation)+360, 360)
				if rot == 90 || rot == 270 {
					w, h = h, w
				}
				l := spLabel{ID: t.ID, Ref: ref, W: w, H: h, X: t.X, Y: t.Y, Rotation: rot, Text: t.Text,
					Box: pcbRect{MinX: t.BBox.MinX, MinY: t.BBox.MinY, MaxX: t.BBox.MaxX, MaxY: t.BBox.MaxY}}
				if body, ok := bodies[ref]; ok {
					l.Comp = body
				}
				isDesig := t.Kind == "attribute" && t.Key == "Designator" && ref != ""
				l.Fixed = !isDesig || (len(want) > 0 && !want[strings.ToUpper(ref)]) || (rot != 0 && rot != 90)
				labels = append(labels, l)
			}
			// New text uses the measured extent of the current designators: their
			// font is what --font-family/--font-size were set to (Arial 45, exam).
			perChar, textH, fontSize := 0.0, 0.0, 0.0
			for _, t := range b.Silk {
				if t.Kind == "attribute" && t.Key == "Designator" && t.BBox != nil && len(t.Text) > 0 && math.Mod(math.Round(t.Rotation)+360, 360) == 0 {
					perChar = math.Max(perChar, (t.BBox.MaxX-t.BBox.MinX)/float64(len(t.Text)))
					textH = math.Max(textH, t.BBox.MaxY-t.BBox.MinY)
					fontSize = math.Max(fontSize, t.FontSize)
				}
			}
			newText := func(text string) (float64, float64, error) {
				if perChar == 0 {
					return 0, 0, fmt.Errorf("no upright designator in the dump to measure the text size from")
				}
				return perChar * float64(len(text)), textH, nil
			}
			padRect := map[string]pcbRect{}
			compPads := map[string][]string{}
			for _, cp := range b.Components {
				for _, p := range cp.Pads {
					if p.W > 0 && p.H > 0 {
						key := strings.ToUpper(cp.Designator + "." + p.Number)
						padRect[key] = pcbRect{MinX: p.X - p.W/2, MinY: p.Y - p.H/2, MaxX: p.X + p.W/2, MaxY: p.Y + p.H/2}
						compPads[cp.Designator] = append(compPads[cp.Designator], key)
					}
				}
			}
			for _, h := range hints {
				ref, text, ok := strings.Cut(h, "=")
				r, found := padRect[strings.ToUpper(ref)]
				if !ok || !found || text == "" {
					return fmt.Errorf("--hint %q: want PAD=text with an existing pad, e.g. J1.1=3V3", h)
				}
				w, hh, err := newText(text)
				if err != nil {
					return err
				}
				comp := strings.SplitN(ref, ".", 2)[0]
				l := spLabel{Ref: comp, Comp: r, W: w, H: hh, Text: text, Free: true}
				for _, k := range compPads[comp] {
					if k != strings.ToUpper(ref) {
						l.Siblings = append(l.Siblings, padRect[k])
					}
				}
				labels = append(labels, l)
			}
			for _, n := range notes {
				text, at, ok := strings.Cut(n, "@")
				var tx, ty float64
				if _, err := fmt.Sscanf(at, "%g,%g", &tx, &ty); !ok || err != nil || text == "" {
					return fmt.Errorf("--note %q: want text@x,y", n)
				}
				w, hh, err := newText(text)
				if err != nil {
					return err
				}
				labels = append(labels, spLabel{Ref: "note", Comp: pcbRect{MinX: tx, MinY: ty, MaxX: tx, MaxY: ty}, W: w, H: hh, Text: text, Note: true, Free: true})
			}
			o.Rotations = []float64{0}
			if allow90 {
				o.Rotations = append(o.Rotations, 90)
			}
			bb := b.Outline.BBox
			placedL, failed := planSilk(labels, pads, bodies, pcbRect{MinX: bb.MinX, MinY: bb.MinY, MaxX: bb.MaxX, MaxY: bb.MaxY}, o)
			var steps []map[string]any
			for i, p := range placedL {
				if p.ID == "" { // new hint / note text
					steps = append(steps, map[string]any{"id": fmt.Sprintf("text-%d-%s", i, p.Ref), "action": "pcb.silk.add",
						"payload": map[string]any{"text": p.Text, "x": p.X, "y": p.Y, "layer": 3, "rotation": p.Rotation, "fontSize": fontSize, "fontFamily": fontFamily}})
					continue
				}
				steps = append(steps, map[string]any{"id": fmt.Sprintf("silk-%d-%s", i, p.Ref), "action": "pcb.silk.set",
					"payload": map[string]any{"primitiveIds": []string{p.ID}, "x": p.X, "y": p.Y, "rotation": p.Rotation}})
			}
			if err := peWrite(out, map[string]any{"units": "mil", "options": o, "placed": placedL, "unplaced": failed}); err != nil {
				return err
			}
			if err := peWrite(strings.TrimSuffix(out, ".json")+".playbook.json", pePlaybook("silk-plan", project, doc, steps)); err != nil {
				return err
			}
			fmt.Fprintf(stdout, "silk-plan: %d placed, %d without a legal slot %v → %s (+ .playbook.json)\n", len(placedL), len(failed), failed, out)
			if len(failed) > 0 {
				return fmt.Errorf("%d designator(s) have no legal slot: %v — loosen gaps or move parts; nothing is forced", len(failed), failed)
			}
			return nil
		},
	}
	c.Flags().StringVar(&board, "board", "", "fresh `pcb dump` JSON (required)")
	c.Flags().StringVar(&out, "out", "", "plan JSON path; the playbook goes next to it (required)")
	c.Flags().StringVar(&project, "apply-project", "", "bind the playbook to this project")
	c.Flags().StringVar(&doc, "apply-doc", "", "bind the playbook to this PCB document")
	c.Flags().StringSliceVar(&only, "only", nil, "plan only these designators (others stay put as obstacles)")
	c.Flags().BoolVar(&allow90, "allow-90", false, "also try 90° text (two reading directions)")
	c.Flags().StringArrayVar(&hints, "hint", nil, "add an interface hint next to a pad: PAD=text, e.g. J1.1=3V3 (repeatable)")
	c.Flags().StringArrayVar(&notes, "note", nil, "add free text near a point: text@x,y, e.g. 'ESP32-S3R8 V1.0@300,-400' (repeatable)")
	c.Flags().StringVar(&fontFamily, "font-family", "Arial", "font of new hint/note text (size follows the measured designators)")
	c.Flags().Float64Var(&o.PadGap, "pad-gap", 6, "gap from any pad (mil)")
	c.Flags().Float64Var(&o.BodyGap, "body-gap", 3, "gap from any component body bbox (mil)")
	c.Flags().Float64Var(&o.LabelGap, "label-gap", 4, "gap between labels (mil)")
	c.Flags().Float64Var(&o.EdgeGap, "edge-gap", 15, "gap from the board outline bbox (mil)")
	c.Flags().Float64Var(&o.MaxDist, "max-dist", 120, "farthest a label may sit from its part (mil)")
	c.Flags().Float64Var(&o.Step, "step", 6, "search step (mil)")
	c.Flags().Float64Var(&o.RotPenalty, "rot-penalty", 25, "extra cost of a 90° label (mil)")
	_ = c.MarkFlagRequired("board")
	_ = c.MarkFlagRequired("out")
	return c
}
