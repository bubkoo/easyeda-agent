package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

const max3DBytes = 20 << 20

func newPcbExport3DCmd(cfg *appConfig, window *string, stdout, stderr io.Writer) *cobra.Command {
	var format, mode, out string
	var placeholders bool
	var elements []string
	c := &cobra.Command{
		Use: "export-3d", Short: "Export real PCB OBJ/STEP with version-bound readbacks and file integrity checks",
		Long:    "Export the active PCB using official beta get3DFile. Requires --window and a NEW --out directory.\nCaptures full board readbacks before/after and refuses changed versions or unknown observations.\nPreserves the official file (including ZIP containers), checks SHA-256, size and model syntax.\nModel bindings do not prove exported coverage; inspect the resulting 3D independently.\nNo save/reload or design changes. Files over 20 MiB are refused before transfer.\nSTEP only includes components imported as STEP. Placeholders default off.",
		Args:    cobra.NoArgs,
		Example: "  easyeda pcb export-3d --format obj --project <uuid> --doc <pcb-uuid> --window <id> --out ./board-3d\n  easyeda pcb export-3d --format step --window <id> --out ./board-step",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if *window == "" || out == "" {
				return fmt.Errorf("--window and a new --out directory are required")
			}
			if format != "obj" && format != "step" {
				return fmt.Errorf("--format must be obj or step")
			}
			if mode != "Outfit" && mode != "Parts" {
				return fmt.Errorf("--model-mode must be Outfit or Parts")
			}
			allowed := map[string]bool{"Component Model": true, "Via": true, "Silkscreen": true, "Wire In Signal Layer": true}
			if len(elements) == 0 || len(elements) > len(allowed) {
				return fmt.Errorf("--elements needs 1 to 4 official element names")
			}
			seen := map[string]bool{}
			for _, element := range elements {
				if !allowed[element] || seen[element] {
					return fmt.Errorf("invalid or duplicate --elements value %q", element)
				}
				seen[element] = true
			}
			if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
				return err
			}
			if err := os.Mkdir(out, 0o755); err != nil {
				return fmt.Errorf("--out must be new: %w", err)
			}
			writeJSON := func(name string, v any) error {
				b, err := json.MarshalIndent(v, "", "  ")
				if err != nil {
					return err
				}
				return os.WriteFile(filepath.Join(out, name), append(b, '\n'), 0o644)
			}
			opts := boardSnapshotOpts{withSilk: true, withRules: true, withLayers: true, withCopper: true}
			fmt.Fprintln(stderr, "Reading target PCB before 3D export…")
			before, err := fetchBoardSnapshot(cfg, *window, opts)
			if before != nil {
				if e := writeJSON("board-before.json", before); e != nil {
					return e
				}
			}
			if err != nil {
				return err
			}
			if err := valid3DBoard(before, *window); err != nil {
				return err
			}
			if cfg.doc != "" && cfg.doc != before.Capture.DocumentUUID {
				docs, _, _, err := discoverDocs(cfg, *window)
				if err != nil {
					return err
				}
				target, err := resolveDoc(docs, cfg.doc)
				if err != nil {
					return err
				}
				if target.UUID != before.Capture.DocumentUUID {
					return fmt.Errorf("requested --doc is not active; use doc switch before exporting")
				}
			}
			fmt.Fprintln(stderr, "Exporting official 3D model (up to 180 seconds)…")
			res, err := requestActionTimed(cfg, "pcb.export.3d", *window, map[string]any{
				"fileType": format, "modelMode": mode, "autoGenerateModels": placeholders, "elements": elements,
			}, 180*time.Second)
			if res != nil {
				if e := writeJSON("export-response.json", res); e != nil {
					return e
				}
			}
			if err != nil {
				return err
			}
			fmt.Fprintln(stderr, "Reading target PCB after 3D export…")
			after, err := fetchBoardSnapshot(cfg, *window, opts)
			if after != nil {
				if e := writeJSON("board-after.json", after); e != nil {
					return e
				}
			}
			if err != nil {
				return err
			}
			if err := valid3DBoard(after, *window); err != nil {
				return err
			}
			if before.SemanticSHA256 != after.SemanticSHA256 || before.Capture.ProjectUUID != after.Capture.ProjectUUID || before.Capture.DocumentUUID != after.Capture.DocumentUUID || before.Capture.TabID != after.Capture.TabID {
				return fmt.Errorf("PCB identity/version changed during export; retained evidence is not a current preview")
			}
			if res.Context == nil || res.Context.ProjectUUID != before.Capture.ProjectUUID || res.Context.DocumentUUID != before.Capture.DocumentUUID || res.Context.DocumentType != "pcb" || res.Context.TabID != before.Capture.TabID || res.WindowID != *window {
				return fmt.Errorf("export response target differs from board readbacks")
			}
			container, err := verify3DArtifact(res, format)
			if err != nil {
				return err
			}
			name := "board." + format
			if container == "zip" {
				name += ".zip"
			}
			if err := saveFirstArtifact(res, filepath.Join(out, name), stderr); err != nil {
				return err
			}
			manifest := map[string]any{"status": "export-verified", "previewVerified": false, "file": name,
				"fileType": format, "container": container, "artifactSha256": res.Artifacts[0].SHA256,
				"artifactSize": res.Artifacts[0].Size, "semanticSha256": before.SemanticSHA256,
				"projectUuid": before.Capture.ProjectUUID, "documentUuid": before.Capture.DocumentUUID,
				"projectName": before.Capture.ProjectName, "modelMode": mode,
				"elements": elements,
				"windowId": *window, "models": res.Result["models"], "modelCoverage": "binding-only",
				"autoGenerateModels": placeholders, "capturedAt": time.Now().UTC().Format(time.RFC3339Nano)}
			if err := writeJSON("manifest.json", manifest); err != nil {
				return err
			}
			return json.NewEncoder(stdout).Encode(manifest)
		},
	}
	c.Flags().StringVar(&format, "format", "obj", "obj | step")
	c.Flags().StringVar(&mode, "model-mode", "Outfit", "Outfit (assembly) | Parts")
	c.Flags().StringSliceVar(&elements, "elements", []string{"Component Model"}, "comma-separated official names: Component Model, Via, Silkscreen, Wire In Signal Layer; board body is implicit")
	c.Flags().BoolVar(&placeholders, "auto-generate-models", false, "request height-based placeholders for unbound models; not real component shapes")
	c.Flags().StringVar(&out, "out", "", "new output directory for original model, readbacks and manifest (required)")
	return c
}

func valid3DBoard(s *boardSnapshot, window string) error {
	if s == nil || s.Capture == nil || !s.Capture.IdentityVerified || s.Capture.WindowID != window || s.Capture.DocumentType != "pcb" || len(s.SemanticSHA256) != 64 || len(s.Components) == 0 || len(s.Partial) != 0 {
		return fmt.Errorf("3D export needs a complete fresh PCB readback with verified identity; inspect board evidence")
	}
	for _, key := range []string{"components", "outline", "layers", "copper"} {
		if s.Availability[key] != "available" {
			return fmt.Errorf("3D export board %s is unknown", key)
		}
	}
	return nil
}

func verify3DArtifact(res *actionResult, format string) (string, error) {
	if res == nil || !res.OK || len(res.Artifacts) != 1 || res.Result["fileType"] != format {
		return "", fmt.Errorf("3D export returned no single matching artifact")
	}
	a := res.Artifacts[0]
	if a.Path == "" || a.Size <= 0 || a.Size > max3DBytes || res.Result["artifactId"] != a.ID {
		return "", fmt.Errorf("3D artifact size/path/identity invalid")
	}
	data, err := os.ReadFile(a.Path)
	if err != nil {
		return "", err
	}
	if int64(len(data)) != a.Size || res.Result["size"] != float64(len(data)) || sha256Hex(data) != a.SHA256 {
		return "", fmt.Errorf("3D artifact size/hash mismatch")
	}
	if bytes.HasPrefix(data, []byte("PK")) {
		z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
		if err != nil {
			return "", err
		}
		found := false
		var total uint64
		names := map[string]bool{}
		if len(z.File) > 4096 {
			return "", fmt.Errorf("too many 3D archive entries")
		}
		for _, f := range z.File {
			if f.FileInfo().IsDir() {
				continue
			}
			if strings.Contains(f.Name, "\\") || strings.HasPrefix(f.Name, "/") || strings.Contains("/"+f.Name+"/", "/../") || strings.Contains(f.Name, ":") {
				return "", fmt.Errorf("unsafe 3D archive path")
			}
			clean := filepath.ToSlash(filepath.Clean(f.Name))
			if names[clean] || f.UncompressedSize64 > 100<<20 {
				return "", fmt.Errorf("duplicate or oversized 3D archive entry")
			}
			names[clean] = true
			total += f.UncompressedSize64
			if total > 100<<20 {
				return "", fmt.Errorf("3D archive expands beyond 100 MiB")
			}
			r, err := f.Open()
			if err != nil {
				return "", err
			}
			b, err := io.ReadAll(io.LimitReader(r, (100<<20)+1))
			r.Close()
			if err != nil {
				return "", err
			}
			if len(b) > 100<<20 {
				return "", fmt.Errorf("3D archive entry exceeds limit")
			}
			ext := strings.ToLower(filepath.Ext(f.Name))
			if ext == "."+format || format == "step" && ext == ".stp" {
				if err := valid3DContent(b, format); err != nil {
					return "", err
				}
				found = true
			}
		}
		if !found {
			return "", fmt.Errorf("3D archive contains no %s model", format)
		}
		return "zip", nil
	}
	return "raw", valid3DContent(data, format)
}

func valid3DContent(data []byte, format string) error {
	text := string(data)
	if format == "step" {
		if !strings.Contains(text, "ISO-10303-21;") || !strings.Contains(text, "END-ISO-10303-21;") || !strings.Contains(text, "DATA;") {
			return fmt.Errorf("invalid STEP model")
		}
		return nil
	}
	vertices, faces := 0, 0
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 {
			continue
		}
		if fields[0] == "v" {
			if len(fields) < 4 {
				return fmt.Errorf("invalid OBJ vertex")
			}
			for _, value := range fields[1:4] {
				n, err := strconv.ParseFloat(value, 64)
				if err != nil || math.IsNaN(n) || math.IsInf(n, 0) {
					return fmt.Errorf("non-finite OBJ vertex")
				}
			}
			vertices++
		}
		if fields[0] == "f" {
			if len(fields) < 4 {
				return fmt.Errorf("invalid OBJ face")
			}
			for _, value := range fields[1:] {
				i, err := strconv.Atoi(strings.Split(value, "/")[0])
				if err != nil || i == 0 {
					return fmt.Errorf("invalid OBJ face index")
				}
			}
			faces++
		}
	}
	if vertices < 3 || faces == 0 {
		return fmt.Errorf("OBJ has no mesh vertices/faces")
	}
	for _, line := range strings.Split(text, "\n") {
		fields := strings.Fields(line)
		if len(fields) == 0 || fields[0] != "f" {
			continue
		}
		for _, value := range fields[1:] {
			i, _ := strconv.Atoi(strings.Split(value, "/")[0])
			if i > vertices || i < -vertices {
				return fmt.Errorf("OBJ face index outside vertex inventory")
			}
		}
	}
	return nil
}
