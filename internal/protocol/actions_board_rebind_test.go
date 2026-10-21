package protocol

import "testing"

// TestBoardRebindRegistered: the connector has handled board.rebind and the
// CLI has shipped `easyeda board rebind` for a while, but the action was never
// added to this catalog — so the daemon's knownActions gate rejected every
// call with UNKNOWN_ACTION. The catalog entry is the fix; this pins it.
func TestBoardRebindRegistered(t *testing.T) {
	for _, a := range AllActions() {
		if a.Name != "board.rebind" {
			continue
		}
		if !a.Mutates {
			t.Error("board.rebind deletes+recreates a Board — must be Mutates=true (drives autosave + doc guard)")
		}
		if a.Domain != DomainBoard {
			t.Errorf("board.rebind domain = %q, want DomainBoard", a.Domain)
		}
		if !a.NeedsWindow {
			t.Error("board.rebind needs a connected window")
		}
		return
	}
	t.Fatal("AllActions() is missing board.rebind — the daemon will reject `easyeda board rebind` with UNKNOWN_ACTION")
}

// Document deletes are destructive and project-level: confirm-gated mutations
// that stay off PCB-window routing (DomainBoard, not DomainPcb).
func TestDocumentDeleteActionsAreConfirmGated(t *testing.T) {
	found := map[string]ActionSpec{}
	for _, a := range AllActions() {
		found[a.Name] = a
	}
	for _, name := range []string{"board.delete", "pcb.document.delete"} {
		a, ok := found[name]
		if !ok {
			t.Fatalf("AllActions() is missing %s — the daemon would reject it with UNKNOWN_ACTION", name)
		}
		if !a.Mutates || !a.NeedsConfirm || !a.NeedsWindow || a.Domain != DomainBoard {
			t.Errorf("%s: mutates=%v needsConfirm=%v needsWindow=%v domain=%q", name, a.Mutates, a.NeedsConfirm, a.NeedsWindow, a.Domain)
		}
	}
}
