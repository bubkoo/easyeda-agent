package app

import (
	"bytes"
	"testing"

	"github.com/spf13/cobra"
)

func runCapturedCmd(t *testing.T, build func(*appConfig) *cobra.Command, args ...string) (*capturedRequest, error) {
	t.Helper()
	cfg, cap, cleanup := newCapturingDaemon(t)
	t.Cleanup(cleanup)
	var stdout, stderr bytes.Buffer
	cmd := build(cfg)
	cmd.SetArgs(args)
	cmd.SetOut(&stdout)
	cmd.SetErr(&stderr)
	return cap, cmd.Execute()
}

// board delete forwards --dry-run / --keep-documents and omits them when unset,
// so a plain delete removes the Board AND its documents.
func TestBoardDelete_WiresDryRunAndKeepDocuments(t *testing.T) {
	board := func(cfg *appConfig) *cobra.Command { return newBoardCmd(cfg, &bytes.Buffer{}, &bytes.Buffer{}) }

	cap, err := runCapturedCmd(t, board, "delete", "--name", "Board1_1", "--dry-run", "--keep-documents")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if cap.action != "board.delete" || cap.payload["name"] != "Board1_1" || cap.payload["dryRun"] != true || cap.payload["keepDocuments"] != true {
		t.Fatalf("got action=%q payload=%v", cap.action, cap.payload)
	}

	cap, err = runCapturedCmd(t, board, "delete", "--name", "Board1_1")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, ok := cap.payload["dryRun"]; ok {
		t.Error("dryRun must be omitted when --dry-run is unset")
	}
	if _, ok := cap.payload["keepDocuments"]; ok {
		t.Error("keepDocuments must be omitted when --keep-documents is unset")
	}
}

// pcb doc-delete needs the exact identity pair before any daemon call.
func TestPcbDocDelete_RequiresUUIDAndExpectedName(t *testing.T) {
	pcb := func(cfg *appConfig) *cobra.Command { return newPcbCmd(cfg, &bytes.Buffer{}, &bytes.Buffer{}) }
	for _, args := range [][]string{
		{"doc-delete", "--uuid", "c0d8d3b9d38f5d76"},
		{"doc-delete", "--expected-name", "PCB1_1"},
	} {
		cap, err := runCapturedCmd(t, pcb, args...)
		if err == nil {
			t.Fatalf("%v: expected an error", args)
		}
		if cap.action != "" {
			t.Fatalf("%v: dispatched %q despite missing identity", args, cap.action)
		}
	}

	cap, err := runCapturedCmd(t, pcb, "doc-delete", "--uuid", "c0d8d3b9d38f5d76", "--expected-name", "PCB1_1", "--dry-run")
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if cap.action != "pcb.document.delete" || cap.payload["uuid"] != "c0d8d3b9d38f5d76" ||
		cap.payload["expectedName"] != "PCB1_1" || cap.payload["dryRun"] != true {
		t.Fatalf("got action=%q payload=%v", cap.action, cap.payload)
	}
}
