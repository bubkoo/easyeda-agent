package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spf13/cobra"
	"github.com/zhoushoujianwork/easyeda-agent/pkg/enclosure"
)

const mmPerMil = 0.0254

// newPcbEnclosureCmd: offline parametric enclosure from a board readback.
// Design: docs/pcb-enclosure-design.md.
func newPcbEnclosureCmd(stdout, stderr io.Writer) *cobra.Command {
	group := &cobra.Command{
		Use:   "enclosure",
		Short: "Generate a printable two-part enclosure (STL) from a board readback (offline)",
		Long: `Offline parametric enclosure: bottom + top shell with lip, screw pillars,
cutouts/pockets and solid bosses, written as binary STL in mm.
  plan   derive an editable spec.json from "pcb dump" (+ "pcb 3d models")
  build  check the spec (component/board clearance, mesh volume) and write STL
Neither command talks to EasyEDA; the enclosure is not an EDA shell object.`,
		Example: `  easyeda pcb dump --project ceshi --out board.json
  easyeda pcb 3d models --project ceshi > models.json
  easyeda pcb enclosure plan --board board.json --models models.json --top-cutout SW1 --out spec.json
  easyeda pcb enclosure build --spec spec.json --out ./enclosure`,
	}
	group.AddCommand(newPcbEnclosurePlanCmd(stdout, stderr), newPcbEnclosureBuildCmd(stdout, stderr))
	return group
}

func newPcbEnclosurePlanCmd(stdout, stderr io.Writer) *cobra.Command {
	var boardPath, modelsPath, out string
	var heights map[string]string
	var pillars []string
	o := enclosure.DefaultPlanOptions()
	c := &cobra.Command{
		Use:   "plan",
		Short: "Derive an editable enclosure spec from a board readback",
		Long: `Read a "pcb dump" board file (and optionally "pcb 3d models" output for
component heights) and write spec.json: outline, thicknesses, split height,
lip, screw pillars at mounting holes, side openings for edge connectors
(J/CN/USB/CON/P) and top openings for --top-cutout parts. Every derived item
names its source; unknown heights use --default-height and are listed in
warnings — fix them with --height REF=mm so the total height is recomputed. Review and edit the spec, then run "pcb enclosure build".`,
		Args: cobra.NoArgs,
		Example: `  easyeda pcb enclosure plan --board board.json --models models.json --out spec.json
  easyeda pcb enclosure plan --board board.json --mode rect --wall 2.4 --top-cutout SW1,SW2 --out spec.json`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if boardPath == "" || out == "" {
				return fmt.Errorf("--board and --out are required")
			}
			if _, err := os.Stat(out); err == nil {
				return fmt.Errorf("--out %s already exists; choose a new file", out)
			}
			if o.Type == "slide" && !cmd.Flags().Changed("mode") {
				o.Mode = "rect"
			}
			for i, raw := range pillars {
				p, err := parsePillarFlag(raw, o.Screw)
				if err != nil {
					return err
				}
				p.ID = fmt.Sprintf("P%d", i+1)
				o.Pillars = append(o.Pillars, p)
			}
			o.Heights = map[string]float64{}
			for ref, v := range heights {
				h, err := strconv.ParseFloat(strings.TrimSuffix(v, "mm"), 64)
				if err != nil {
					return fmt.Errorf("--height %s=%s: not a number in mm", ref, v)
				}
				o.Heights[ref] = h
			}
			in, err := enclosurePlanInput(boardPath, modelsPath)
			if err != nil {
				return err
			}
			spec, err := enclosure.Plan(in, o)
			if err != nil {
				return err
			}
			b, err := json.MarshalIndent(spec, "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(out, append(b, '\n'), 0o644); err != nil {
				return err
			}
			for _, w := range spec.Warnings {
				fmt.Fprintln(stderr, "⚠ "+w)
			}
			return writeJSONLine(stdout, map[string]any{"status": "planned", "spec": out, "height": spec.Height,
				"split": spec.Split, "pillars": len(spec.Pillars), "features": len(spec.Features),
				"components": len(spec.Components), "candidates": spec.Candidates, "warnings": spec.Warnings})
		},
	}
	f := c.Flags()
	f.StringVar(&boardPath, "board", "", `board file from "pcb dump --out" (required)`)
	f.StringVar(&modelsPath, "models", "", `output of "pcb 3d models" for component heights`)
	f.StringVar(&out, "out", "", "new spec.json path (required)")
	f.StringVar(&o.Mode, "mode", o.Mode, "outline: board (follow the board) or rect (its bounding box)")
	f.Float64Var(&o.Resolution, "resolution", o.Resolution, "XY grid in mm (0.02..1)")
	f.Float64Var(&o.Wall, "wall", o.Wall, "wall thickness mm")
	f.Float64Var(&o.Floor, "floor", o.Floor, "bottom thickness mm")
	f.Float64Var(&o.Lid, "lid", o.Lid, "top thickness mm")
	f.Float64Var(&o.Thickness, "thickness", o.Thickness, "PCB thickness mm")
	f.Float64Var(&o.Standoff, "standoff", o.Standoff, "gap between floor and PCB bottom mm (raised for bottom-side parts)")
	f.Float64Var(&o.Clearance, "clearance", o.Clearance, "gap between board edge and wall mm")
	f.Float64Var(&o.TopClearance, "top-clearance", o.TopClearance, "gap above the tallest part mm")
	f.Float64Var(&o.DefaultHeight, "default-height", o.DefaultHeight, "height for parts without a known height mm")
	f.Float64Var(&o.EdgeMargin, "edge-margin", o.EdgeMargin, "max distance from the board edge for a connector opening mm")
	f.Float64Var(&o.CutoutMargin, "cutout-margin", o.CutoutMargin, "extra size around openings mm")
	f.StringVar(&o.Screw, "screw", o.Screw, "pillar screw when the footprint does not say: M2|M2.5|M3|M4|M5|M6")
	f.StringVar(&o.Type, "type", o.Type, "split (bottom + top shell) or slide (box + sliding lid, rectangular)")
	f.StringVar(&o.SlideSide, "slide-side", o.SlideSide, "side the slide lid comes out of: x-|x+|y-|y+")
	f.StringArrayVar(&pillars, "pillar", nil, "add a screw pillar at board mm coordinates: X,Y[,M3] (repeatable)")
	f.StringVar(&o.Snaps, "snaps", o.Snaps, "auto (four lip snaps on split shells without pillars) or none")
	f.StringToStringVar(&heights, "height", nil, "component height overrides in mm, e.g. J1=8.5,U1=0.9")
	f.StringSliceVar(&o.TopCutouts, "top-cutout", nil, "designators that get an opening in the lid (or floor for bottom parts)")
	return c
}

func writeJSONLine(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(w, string(b))
	return err
}

// enclosurePlanInput converts a board snapshot (mil) and optional model list
// into the enclosure package's mm input.
func enclosurePlanInput(boardPath, modelsPath string) (enclosure.PlanInput, error) {
	var in enclosure.PlanInput
	data, err := os.ReadFile(boardPath)
	if err != nil {
		return in, err
	}
	var snap boardSnapshot
	if err := json.Unmarshal(data, &snap); err != nil {
		return in, fmt.Errorf("--board: %w", err)
	}
	if snap.Outline == nil {
		return in, fmt.Errorf("--board has no outline; dump with the PCB in the foreground")
	}
	pts := snap.Outline.Points
	if snap.Outline.Source != "polygon" || len(pts) < 3 {
		b := snap.Outline.BBox
		pts = [][2]float64{{b.MinX, b.MinY}, {b.MaxX, b.MinY}, {b.MaxX, b.MaxY}, {b.MinX, b.MaxY}}
	}
	for _, p := range pts {
		in.Board = append(in.Board, enclosure.Point{p[0] * mmPerMil, p[1] * mmPerMil})
	}
	models := map[string]map[string]any{}
	if modelsPath != "" {
		if models, err = readModels3D(modelsPath); err != nil {
			return in, err
		}
	}
	var missing []string
	for _, c := range snap.Components {
		if c.BBox == nil {
			missing = append(missing, c.Designator)
			continue
		}
		side := "top"
		if c.Layer == 2 {
			side = "bottom"
		}
		pc := enclosure.PlanComponent{Ref: c.Designator, Footprint: footprintName(c.Footprint), Side: side,
			BBox:    [4]float64{c.BBox.MinX * mmPerMil, c.BBox.MinY * mmPerMil, c.BBox.MaxX * mmPerMil, c.BBox.MaxY * mmPerMil},
			AnchorX: c.X * mmPerMil, AnchorY: c.Y * mmPerMil}
		if m := models[c.Designator]; m != nil {
			if model, ok := m["model"].(map[string]any); ok {
				pc.ModelName, _ = model["name"].(string)
			}
			pc.HeightAttr, _ = m["height"].(string)
			if pc.Footprint == "" {
				pc.Footprint = footprintName(m["footprint"])
			}
		}
		in.Components = append(in.Components, pc)
	}
	if len(missing) > 0 {
		return in, fmt.Errorf("--board components without bbox (cannot check clearance): %s; re-run pcb dump", strings.Join(missing, ", "))
	}
	in.Source = &enclosure.Source{BoardSHA256: snap.SemanticSHA256}
	if snap.Capture != nil {
		in.Source.Project, in.Source.DocumentUUID = snap.Capture.ProjectName, snap.Capture.DocumentUUID
	}
	return in, nil
}

func footprintName(v any) string {
	switch f := v.(type) {
	case string:
		return f
	case map[string]any:
		s, _ := f["name"].(string)
		return s
	}
	return ""
}

// readModels3D accepts the raw "pcb 3d models" response or its result object.
func readModels3D(path string) (map[string]map[string]any, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc struct {
		Result     *struct{ Components []map[string]any } `json:"result"`
		Components []map[string]any                       `json:"components"`
	}
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("--models: %w", err)
	}
	list := doc.Components
	if doc.Result != nil {
		list = doc.Result.Components
	}
	if len(list) == 0 {
		return nil, fmt.Errorf("--models has no components; pass the output of pcb 3d models")
	}
	out := map[string]map[string]any{}
	for _, c := range list {
		if ref, _ := c["designator"].(string); ref != "" {
			out[ref] = c
		}
	}
	return out, nil
}

func newPcbEnclosureBuildCmd(stdout, stderr io.Writer) *cobra.Command {
	var specPath, out string
	var step bool
	c := &cobra.Command{
		Use:   "build",
		Short: "Check an enclosure spec and write bottom.stl / top.stl",
		Long: `Validate spec.json, rasterize both shells, check that no component keep-out
box or the board slab intersects the shell and that each mesh volume matches
its raster, then write <part>.stl (bottom/top, or box/lid for a slide box),
preview.png and manifest.json into a NEW --out directory; --step also writes
<part>.step (faceted B-rep). Lip snaps, part overlap and the slide-out path are
checked too; spans thinner than 0.8 mm are warnings. On any failed check only
manifest.json and preview.png are written and the command exits non-zero.`,
		Args:    cobra.NoArgs,
		Example: `  easyeda pcb enclosure build --spec spec.json --out ./enclosure`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if specPath == "" || out == "" {
				return fmt.Errorf("--spec and --out are required")
			}
			data, err := os.ReadFile(specPath)
			if err != nil {
				return err
			}
			spec, err := enclosure.Parse(data)
			if err != nil {
				return err
			}
			res, err := enclosure.Build(spec)
			if err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			if err := os.Mkdir(out, 0o755); err != nil {
				return fmt.Errorf("--out must be a new directory: %w", err)
			}
			files := map[string]string{}
			for _, p := range res.Parts {
				if p.STL == nil {
					continue
				}
				name := p.Name + ".stl"
				if err := os.WriteFile(filepath.Join(out, name), p.STL, 0o644); err != nil {
					return err
				}
				files[name] = sha256Hex(p.STL)
				if step {
					data, err := p.STEP(p.Name)
					if err != nil {
						return err
					}
					if err := os.WriteFile(filepath.Join(out, p.Name+".step"), data, 0o644); err != nil {
						return err
					}
					files[p.Name+".step"] = sha256Hex(data)
				}
			}
			if img, err := enclosure.Preview(res.Parts, 1000, 620); err != nil {
				return err
			} else if err := os.WriteFile(filepath.Join(out, "preview.png"), img, 0o644); err != nil {
				return err
			}
			status := "built"
			if !res.OK {
				status = "checks-failed"
			}
			manifest := map[string]any{"status": status, "spec": specPath, "specSha256": sha256Hex(data),
				"source": spec.Source, "height": spec.Height, "split": spec.Split, "files": files, "result": res}
			b, err := json.MarshalIndent(manifest, "", "  ")
			if err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(out, "manifest.json"), append(b, '\n'), 0o644); err != nil {
				return err
			}
			for _, w := range res.Warnings {
				fmt.Fprintln(stderr, "⚠ "+w)
			}
			if err := writeJSONLine(stdout, map[string]any{"status": status, "out": out, "files": files, "checks": res.Checks}); err != nil {
				return err
			}
			if !res.OK {
				return fmt.Errorf("enclosure checks failed; see %s", filepath.Join(out, "manifest.json"))
			}
			return nil
		},
	}
	c.Flags().StringVar(&specPath, "spec", "", "enclosure spec.json (required)")
	c.Flags().StringVar(&out, "out", "", "new output directory (required)")
	c.Flags().BoolVar(&step, "step", false, "also write <part>.step (faceted B-rep, not yet verified in a CAD tool)")
	return c
}

// parsePillarFlag reads "X,Y[,Mn]" in board millimetres.
func parsePillarFlag(raw, screw string) (enclosure.Pillar, error) {
	parts := strings.Split(raw, ",")
	if len(parts) < 2 || len(parts) > 3 {
		return enclosure.Pillar{}, fmt.Errorf("--pillar %q: want X,Y[,M3]", raw)
	}
	x, ex := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	y, ey := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	if ex != nil || ey != nil {
		return enclosure.Pillar{}, fmt.Errorf("--pillar %q: X and Y must be numbers in mm", raw)
	}
	if len(parts) == 3 {
		screw = strings.ToUpper(strings.TrimSpace(parts[2]))
	}
	return enclosure.Pillar{X: x, Y: y, Screw: screw, Source: "manual"}, nil
}
