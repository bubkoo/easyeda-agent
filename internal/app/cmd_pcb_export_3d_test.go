package app

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

const mesh3D = "v 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3\n"

func model3DResult(t *testing.T, data []byte) *actionResult {
	t.Helper()
	p := filepath.Join(t.TempDir(), "model")
	if err := os.WriteFile(p, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return &actionResult{OK: true, Result: map[string]any{"fileType": "obj", "size": float64(len(data)), "artifactId": "a"}, Artifacts: []artifactRef{{ID: "a", Path: p, Size: int64(len(data)), SHA256: sha256Hex(data)}}}
}

func Test3DArtifactIntegrityAndMesh(t *testing.T) {
	r := model3DResult(t, []byte(mesh3D))
	if kind, err := verify3DArtifact(r, "obj"); err != nil || kind != "raw" {
		t.Fatalf("%s %v", kind, err)
	}
	r.Artifacts[0].SHA256 = "bad"
	if _, err := verify3DArtifact(r, "obj"); err == nil {
		t.Fatal("corrupt model accepted")
	}
	for _, model := range []string{"<html>error</html>", "v NaN 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 3", "v 0 0 0\nv 1 0 0\nv 0 1 0\nf 1 2 99"} {
		if err := valid3DContent([]byte(model), "obj"); err == nil {
			t.Fatal("invalid mesh accepted")
		}
	}
}

func Test3DArchiveValidation(t *testing.T) {
	for _, name := range []string{"models/board.obj", "../board.obj", "error.txt"} {
		var b bytes.Buffer
		z := zip.NewWriter(&b)
		w, _ := z.Create(name)
		w.Write([]byte(mesh3D))
		z.Close()
		kind, err := verify3DArtifact(model3DResult(t, b.Bytes()), "obj")
		if name == "models/board.obj" {
			if err != nil || kind != "zip" {
				t.Fatalf("%s %v", kind, err)
			}
		} else if err == nil {
			t.Fatal("unsafe or missing model accepted")
		}
	}
}

func Test3DReadbackRejectsUnknown(t *testing.T) {
	s := &boardSnapshot{SemanticSHA256: sha256Hex([]byte("board")), Components: []boardComp{{}}, Capture: &pcbCaptureEvidence{IdentityVerified: true, WindowID: "w", DocumentType: "pcb"}, Availability: map[string]string{"components": "available", "outline": "available", "layers": "available", "copper": "available"}}
	if err := valid3DBoard(s, "w"); err != nil {
		t.Fatal(err)
	}
	s.Availability["copper"] = "unknown"
	if err := valid3DBoard(s, "w"); err == nil {
		t.Fatal("unknown copper accepted")
	}
}
