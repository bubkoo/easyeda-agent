package app

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

// The official EasyEDA project tree keeps listing documents removed through
// dmt_Board.deleteBoard / dmt_Pcb.deletePcb / dmt_Schematic.deleteSchematic
// until the Web page reloads (live 2026-10-21: board.delete was proven clean by
// readback, yet the tree still showed the PCB, schematic and both pages). The
// pro-api-types SDK (0.4.25) exposes no tree/document-list refresh, so a
// verified document delete is followed by the same save → page reload →
// restore → readback flow as `easyeda web reload`.

// treeRefreshTimeout bounds the reload, reconnect and readback after a delete.
const treeRefreshTimeout = 30 * time.Second

// refreshProjectTree reloads the Web page for the window's current project and
// active document. A variable so command tests can observe it without a host.
var refreshProjectTree = func(cfg *appConfig, window string) (map[string]any, error) {
	pinned := *cfg
	pinned.doc = ""
	project, err := requestAction(&pinned, "project.current", window, nil)
	if err != nil {
		return nil, fmt.Errorf("read current project: %w", err)
	}
	doc, err := requestAction(&pinned, "document.current", window, nil)
	if err != nil {
		return nil, fmt.Errorf("read active document: %w", err)
	}
	projectUUID, _ := project.Result["uuid"].(string)
	docUUID, _ := doc.Result["uuid"].(string)
	if projectUUID == "" || docUUID == "" {
		return nil, fmt.Errorf("no exact current project/active document to restore after the reload")
	}
	pinned.project, pinned.doc = projectUUID, docUUID
	return reloadWebPage(&pinned, window, treeRefreshTimeout)
}

// dispatchDocumentDelete runs board.delete / pcb.document.delete and, once the
// delete is verified, refreshes the official project tree. The refresh outcome
// is merged into the result as treeRefreshed + treeRefresh and never turns a
// verified delete into a failure. Dry runs, unbind-only deletes and partial
// deletes do not refresh.
func dispatchDocumentDelete(cfg *appConfig, action, window string, payload map[string]any, refresh bool, stdout, stderr io.Writer) error {
	var buf bytes.Buffer
	err := dispatch(cfg, action, window, payload, &buf, stderr)
	if err != nil || payload["dryRun"] == true || payload["keepDocuments"] == true {
		_, _ = stdout.Write(buf.Bytes())
		return err
	}
	var envelope map[string]any
	if json.Unmarshal(buf.Bytes(), &envelope) != nil {
		_, _ = stdout.Write(buf.Bytes())
		return nil
	}
	result, _ := envelope["result"].(map[string]any)
	if result == nil || result["verified"] != true {
		_, _ = stdout.Write(buf.Bytes())
		return nil
	}
	report := map[string]any{"method": "page-reload"}
	if !refresh {
		report["skipped"] = "disabled by --no-tree-refresh"
	} else {
		fmt.Fprintln(stderr, "↻ refreshing the project tree: saving the active document and reloading the Web page…")
		reload, rerr := refreshProjectTree(cfg, window)
		if rerr != nil {
			report["error"] = rerr.Error()
			fmt.Fprintf(stderr, "⚠ delete verified, but the project tree was not refreshed: %v\n", rerr)
		} else {
			report["reload"] = reload
		}
	}
	result["treeRefreshed"] = report["reload"] != nil
	result["treeRefresh"] = report
	out, merr := json.MarshalIndent(envelope, "", "  ")
	if merr != nil {
		_, _ = stdout.Write(buf.Bytes())
		return nil
	}
	_, _ = stdout.Write(append(out, '\n'))
	return nil
}
