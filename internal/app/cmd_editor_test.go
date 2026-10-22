package app

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

func TestEditorPanels_ValidatesAndWiresPayload(t *testing.T) {
	build := func(cfg *appConfig) *cobra.Command { return newEditorCmd(cfg, &bytes.Buffer{}, &bytes.Buffer{}) }
	for _, args := range [][]string{{"panels"}, {"panels", "--left", "hidden"}} {
		cap, err := runCapturedCmd(t, build, args...)
		if err == nil || cap.action != "" {
			t.Fatalf("%v: expected refusal before dispatch, got err=%v action=%q", args, err, cap.action)
		}
	}
	cap, err := runCapturedCmd(t, build, "panels", "--left", "closed", "--right", "open")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if cap.action != "editor.panels" || cap.payload["left"] != "closed" || cap.payload["right"] != "open" {
		t.Fatalf("got action=%q payload=%v", cap.action, cap.payload)
	}
	cap, _ = runCapturedCmd(t, build, "panels", "--right", "closed")
	if _, ok := cap.payload["left"]; ok || cap.payload["right"] != "closed" {
		t.Fatalf("unset side must be omitted: %v", cap.payload)
	}
}
