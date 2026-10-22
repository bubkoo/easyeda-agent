package app

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// newPcb3DCmd groups per-instance 3D model work on an existing PCB. Whole-board
// export stays at `pcb export-3d`. Design: docs/pcb-3d-design.md.
func newPcb3DCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	group := &cobra.Command{
		Use:   "3d",
		Short: "Inspect and adjust component 3D models on the active PCB",
		Long: `Per-component 3D model work on an existing PCB through typed actions.
  models        list each component's bound model, placement and Height (read-only)
  transform     change named size/rotation/offset axes of one component's model
  bind          bind/replace one component's model with an existing library model
  export-shell  export the enclosure designed in the EasyEDA shell editor
Whole-board OBJ/STEP export is "pcb export-3d". Placement is the host's
otherProperty["3D Model Transform"] (size XYZ, rotation XYZ, offset XYZ in
mil/deg; mapping inferred from live samples). Writes verify a fresh readback;
confirm persistence with pcb save, doc reload, pcb 3d models.`,
	}
	group.AddCommand(newPcb3DModelsCmd(cfg, window, stdout, stderr))
	group.AddCommand(newPcb3DTransformCmd(cfg, window, stdout, stderr))
	group.AddCommand(newPcb3DBindCmd(cfg, window, stdout, stderr))
	group.AddCommand(newPcb3DExportShellCmd(cfg, window, stdout, stderr))
	return group
}

func newPcb3DModelsCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var missing bool
	c := &cobra.Command{
		Use:   "models",
		Short: "List component 3D model bindings, placement and coverage (read-only)",
		Long: `Read every component on the active PCB: designator, layer, footprint, bound
3D model (or none), parsed placement and the Height attribute the official
exporter uses for placeholders. summary counts bound/unbound/unknown; an
unreadable component counts as unknown and is never dropped. --missing keeps
only components that are not bound (summary stays whole-board).`,
		Args: cobra.NoArgs,
		Example: `  easyeda pcb 3d models --project ceshi
  easyeda pcb 3d models --missing --project ceshi`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if !missing {
				return dispatch(cfg, "pcb.model3d.list", *window, map[string]any{}, stdout, stderr)
			}
			res, err := requestAction(cfg, "pcb.model3d.list", *window, map[string]any{})
			if err != nil {
				return err
			}
			res.Result["components"] = filterUnbound3D(res.Result["components"])
			res.Result["filter"] = "missing"
			b, err := json.MarshalIndent(res, "", "  ")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(stdout, string(b))
			return err
		},
	}
	c.Flags().BoolVar(&missing, "missing", false, "only list components without a bound model (unbound or unknown)")
	return c
}

func filterUnbound3D(raw any) []any {
	out := []any{}
	list, _ := raw.([]any)
	for _, item := range list {
		if m, ok := item.(map[string]any); ok && m["status"] != "bound" {
			out = append(out, item)
		}
	}
	return out
}

// transformAxes are the CLI flags of the nine placement numbers, in host order.
var transformAxes = []struct{ flag, group, axis string }{
	{"size-x", "size", "x"}, {"size-y", "size", "y"}, {"size-z", "size", "z"},
	{"rotate-x", "rotation", "x"}, {"rotate-y", "rotation", "y"}, {"rotate-z", "rotation", "z"},
	{"offset-x", "offset", "x"}, {"offset-y", "offset", "y"}, {"offset-z", "offset", "z"},
}

func newPcb3DTransformCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var ref, unit string
	var dryRun bool
	values := map[string]*float64{}
	c := &cobra.Command{
		Use:   "transform",
		Short: "Set named size/rotation/offset axes of one component's 3D model",
		Long: `Change only the given axes of one component's model placement; every other
axis and every other attribute is kept. Sizes/offsets use --unit (mil or mm,
default mm); rotations are degrees. The component must already have a model
placement (see pcb 3d models). The write re-asserts the designator, never
writes projected keys, and fails as partial if any other attribute changed in
the fresh readback. --dry-run prints before/after without writing.`,
		Args: cobra.NoArgs,
		Example: `  easyeda pcb 3d transform --ref J1 --offset-z -3 --dry-run --project ceshi --doc PCB1
  easyeda pcb 3d transform --ref ANT1 --rotate-x 180 --project ceshi --doc PCB1`,
		RunE: func(cmd *cobra.Command, args []string) error {
			payload, err := transformPayload(cmd, ref, unit, dryRun, values)
			if err != nil {
				return err
			}
			return dispatch(cfg, "pcb.model3d.set_transform", *window, payload, stdout, stderr)
		},
	}
	c.Flags().StringVar(&ref, "ref", "", "component designator (required, must match exactly one)")
	c.Flags().StringVar(&unit, "unit", "mm", "unit for size/offset values: mm or mil")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show the resulting placement without writing")
	for _, a := range transformAxes {
		values[a.flag] = c.Flags().Float64(a.flag, 0, a.group+" "+strings.ToUpper(a.axis))
	}
	return c
}

func transformPayload(cmd *cobra.Command, ref, unit string, dryRun bool, values map[string]*float64) (map[string]any, error) {
	if strings.TrimSpace(ref) == "" {
		return nil, fmt.Errorf("--ref is required")
	}
	if unit != "mm" && unit != "mil" {
		return nil, fmt.Errorf("--unit must be mm or mil")
	}
	patch := map[string]map[string]float64{}
	for _, a := range transformAxes {
		if !cmd.Flags().Changed(a.flag) {
			continue
		}
		v := *values[a.flag]
		if a.group != "rotation" && unit == "mm" {
			v *= milPerMM
		}
		if patch[a.group] == nil {
			patch[a.group] = map[string]float64{}
		}
		patch[a.group][a.axis] = v
	}
	if len(patch) == 0 {
		return nil, fmt.Errorf("set at least one of --size-*, --rotate-*, --offset-*")
	}
	return map[string]any{"designator": ref, "patch": patch, "dryRun": dryRun}, nil
}

func newPcb3DBindCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var ref, model, library string
	var dryRun bool
	c := &cobra.Command{
		Use:   "bind",
		Short: "Bind or replace one component's 3D model with an existing library model",
		Long: `Point one PCB component instance at a 3D model that already exists in a
library (find one with lib model3d search, import one with lib model3d create).
Only this instance changes; the Device library record is untouched (use
lib device set-model3d for that). The current placement is kept, so review
size/offset afterwards with pcb 3d models / pcb 3d transform. Verified by the
instance's model readback; --dry-run shows the change without writing.`,
		Args:    cobra.NoArgs,
		Example: `  easyeda pcb 3d bind --ref U1 --model <modelUuid> --model-library <libraryUuid> --dry-run --project ceshi --doc PCB1`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(ref) == "" || strings.TrimSpace(model) == "" || strings.TrimSpace(library) == "" {
				return fmt.Errorf("--ref, --model and --model-library are required")
			}
			return dispatch(cfg, "pcb.model3d.bind", *window, map[string]any{
				"designator": ref, "model": map[string]any{"uuid": model, "libraryUuid": library}, "dryRun": dryRun,
			}, stdout, stderr)
		},
	}
	c.Flags().StringVar(&ref, "ref", "", "component designator (required, must match exactly one)")
	c.Flags().StringVar(&model, "model", "", "3D model UUID (required)")
	c.Flags().StringVar(&library, "model-library", "", "library UUID holding the model (required)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "show the change without writing")
	return c
}

func newPcb3DExportShellCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var format, out string
	c := &cobra.Command{
		Use:   "export-shell",
		Short: "Export the active PCB's designed 3D enclosure (STL/STEP/OBJ)",
		Long: `Export the 3D shell (enclosure) through official beta get3DShellFile.
The shell must already be designed in the EasyEDA shell editor: no API can
create shell geometry, so a board without one fails with a clear error.
Read-only. Requires --window; --out must be a new file. Size and SHA-256 of
the written file are checked against the transferred artifact.`,
		Args:    cobra.NoArgs,
		Example: `  easyeda pcb 3d export-shell --format stl --window <id> --out ./shell.stl`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if *window == "" || out == "" {
				return fmt.Errorf("--window and --out are required")
			}
			if format != "stl" && format != "step" && format != "obj" {
				return fmt.Errorf("--format must be stl, step or obj")
			}
			if _, err := os.Stat(out); err == nil {
				return fmt.Errorf("--out %s already exists; choose a new file", out)
			}
			res, err := requestActionTimed(cfg, "pcb.export.3d_shell", *window, map[string]any{"fileType": format}, 180*time.Second)
			if err != nil {
				return err
			}
			if err := verifyShellArtifact(res, format); err != nil {
				return err
			}
			if err := saveFirstArtifact(res, out, stderr); err != nil {
				return err
			}
			b, err := json.MarshalIndent(map[string]any{"status": "exported", "file": out, "fileType": format,
				"sha256": res.Artifacts[0].SHA256, "size": res.Artifacts[0].Size, "context": res.Context}, "", "  ")
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(stdout, string(b))
			return err
		},
	}
	c.Flags().StringVar(&format, "format", "stl", "stl, step or obj")
	c.Flags().StringVar(&out, "out", "", "new output file (required)")
	return c
}

func verifyShellArtifact(res *actionResult, format string) error {
	if res == nil || !res.OK || len(res.Artifacts) != 1 || res.Result["fileType"] != format || res.Result["artifactId"] != res.Artifacts[0].ID {
		return fmt.Errorf("3D shell export returned no single matching artifact")
	}
	a := res.Artifacts[0]
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return err
	}
	if len(data) == 0 || int64(len(data)) != a.Size || len(data) > max3DBytes || sha256Hex(data) != a.SHA256 {
		return fmt.Errorf("3D shell artifact size/hash mismatch")
	}
	return nil
}
