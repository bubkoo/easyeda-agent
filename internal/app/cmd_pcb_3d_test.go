package app

import (
	"bytes"
	"encoding/json"
	"io"
	"math"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

func TestPcb3DTransformPayloadConvertsOnlyLengthsAndKeepsUnsetAxes(t *testing.T) {
	cmd := newPcb3DTransformCmd(&appConfig{}, new(string), io.Discard, io.Discard)
	if err := cmd.ParseFlags([]string{"--ref", "J1", "--offset-z", "-3", "--rotate-x", "180"}); err != nil {
		t.Fatal(err)
	}
	values := map[string]*float64{}
	for _, a := range transformAxes {
		v, _ := cmd.Flags().GetFloat64(a.flag)
		values[a.flag] = &v
	}
	p, err := transformPayload(cmd, "J1", "mm", false, values)
	if err != nil {
		t.Fatal(err)
	}
	patch := p["patch"].(map[string]map[string]float64)
	if math.Abs(patch["offset"]["z"]+118.11) > 0.01 || patch["rotation"]["x"] != 180 || len(patch) != 2 || len(patch["offset"]) != 1 {
		t.Fatalf("patch=%v", patch)
	}
}

func TestPcb3DTransformPayloadRejectsBadInput(t *testing.T) {
	for _, tc := range []struct {
		args       []string
		ref, unit  string
		wantSubstr string
	}{
		{nil, "", "mm", "--ref"},
		{[]string{"--size-x", "1"}, "U1", "inch", "--unit"},
		{nil, "U1", "mm", "at least one"},
	} {
		cmd := newPcb3DTransformCmd(&appConfig{}, new(string), io.Discard, io.Discard)
		if err := cmd.ParseFlags(tc.args); err != nil {
			t.Fatal(err)
		}
		_, err := transformPayload(cmd, tc.ref, tc.unit, false, map[string]*float64{"size-x": new(float64)})
		if err == nil || !strings.Contains(err.Error(), tc.wantSubstr) {
			t.Fatalf("args=%v err=%v; want %q", tc.args, err, tc.wantSubstr)
		}
	}
}

func TestPcb3DModelsMissingKeepsUnboundAndUnknown(t *testing.T) {
	var list []any
	_ = json.Unmarshal([]byte(`[{"designator":"R1","status":"bound"},{"designator":"R2","status":"unbound"},{"designator":"R3","status":"unknown"}]`), &list)
	got := filterUnbound3D(list)
	if len(got) != 2 || got[0].(map[string]any)["designator"] != "R2" || got[1].(map[string]any)["designator"] != "R3" {
		t.Fatalf("got %v", got)
	}
}

func TestPcb3DSetTransformPartialFailsDispatch(t *testing.T) {
	for _, tc := range []struct {
		result  string
		failure bool
	}{
		{`{"designator":"J1","partial":true,"verified":false}`, true},
		{`{"designator":"J1","verified":true}`, false},
		{`{"designator":"J1","dryRun":true}`, false},
	} {
		response := `{"ok":true,"result":` + tc.result + `}`
		cfg, _, cleanup := newAutolayoutTestDaemon(t, func(_ int, _ autolayoutTestCall) string { return response })
		var out bytes.Buffer
		for _, action := range []string{"pcb.model3d.set_transform", "pcb.model3d.bind"} {
			err := dispatch(cfg, action, "", map[string]any{}, &out, io.Discard)
			if (err != nil) != tc.failure {
				t.Fatalf("%s %s: err=%v", action, tc.result, err)
			}
		}
		cleanup()
	}
}

func TestPcb3DBindAndShellRequireTargets(t *testing.T) {
	for _, tc := range []struct {
		cmd  func() *cobra.Command
		args []string
		want string
	}{
		{func() *cobra.Command { return newPcb3DBindCmd(&appConfig{}, new(string), io.Discard, io.Discard) }, []string{"--ref", "U1"}, "--model"},
		{func() *cobra.Command {
			return newPcb3DExportShellCmd(&appConfig{}, new(string), io.Discard, io.Discard)
		}, []string{"--out", "x.stl"}, "--window"},
	} {
		c := tc.cmd()
		c.SetArgs(tc.args)
		c.SilenceUsage, c.SilenceErrors = true, true
		if err := c.Execute(); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("args=%v err=%v", tc.args, err)
		}
	}
	w := "win"
	c := newPcb3DExportShellCmd(&appConfig{}, &w, io.Discard, io.Discard)
	c.SetArgs([]string{"--out", t.TempDir(), "--format", "stl"})
	c.SilenceUsage, c.SilenceErrors = true, true
	if err := c.Execute(); err == nil || !strings.Contains(err.Error(), "already exists") {
		t.Fatalf("existing --out accepted: %v", err)
	}
}

func TestVerifyShellArtifactChecksHash(t *testing.T) {
	path := t.TempDir() + "/shell.stl"
	data := []byte("solid shell")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	res := &actionResult{OK: true, Result: map[string]any{"fileType": "stl", "artifactId": "a1"},
		Artifacts: []artifactRef{{ID: "a1", Path: path, Size: int64(len(data)), SHA256: sha256Hex(data)}}}
	if err := verifyShellArtifact(res, "stl"); err != nil {
		t.Fatal(err)
	}
	res.Artifacts[0].SHA256 = "bad"
	if verifyShellArtifact(res, "stl") == nil {
		t.Fatal("hash mismatch accepted")
	}
}
