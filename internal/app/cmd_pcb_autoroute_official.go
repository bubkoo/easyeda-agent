package app

// cmd_pcb_autoroute_official.go — `pcb autoroute` on the official router
// (pcb.route.auto → eda.pcb_Document.autoRouting). The connector keeps existing
// copper and reports a before/after inventory; this side builds the payload,
// prints the router's per-net report and fails when locked copper was lost.

import (
	"fmt"
	"io"
	"time"
)

// buildOfficialAutoroutePayload validates the flags into the pcb.route.auto payload.
func buildOfficialAutoroutePayload(nets, ignoreNets []string, layers []int, corner, optimize string) (map[string]any, error) {
	if corner != "45" && corner != "90" {
		return nil, fmt.Errorf("--corner must be 45 or 90 (got %q)", corner)
	}
	if optimize != "completion" && optimize != "faster" {
		return nil, fmt.Errorf("--optimize must be completion or faster (got %q)", optimize)
	}
	payload := map[string]any{"cornerStyle": corner, "optimization": optimize}
	if len(nets) > 0 {
		payload["nets"] = nets
	}
	if len(ignoreNets) > 0 {
		payload["ignoreNets"] = ignoreNets
	}
	if len(layers) > 0 {
		payload["layers"] = layers
	}
	return payload, nil
}

func runOfficialAutoroute(cfg *appConfig, window string, nets, ignoreNets []string, layers []int, corner, optimize string,
	timeout time.Duration, stdout, stderr io.Writer) error {
	payload, err := buildOfficialAutoroutePayload(nets, ignoreNets, layers, corner, optimize)
	if err != nil {
		return err
	}
	fmt.Fprintf(stderr, "official auto-route (existing copper kept): %v — may take minutes\n", payload)
	res, err := requestActionTimed(cfg, "pcb.route.auto", window, payload, timeout)
	if err != nil {
		return err
	}
	if err := writeJSON(stdout, res.Result); err != nil {
		return err
	}
	if router, ok := res.Result["router"].(map[string]any); ok {
		fmt.Fprintf(stderr, "router: success=%v nets %v/%v routed, failed=%v, %vms\n",
			router["success"], router["successNetsCount"], router["totalNetsCount"], router["failedNets"], router["duration"])
	}
	fmt.Fprintf(stderr, "created: %v; removed: %v\n", res.Result["created"], res.Result["removedPrimitiveIds"])
	if lost, _ := res.Result["lockedLost"].([]any); len(lost) > 0 {
		return fmt.Errorf("the router removed %d locked primitive(s) %v — restore them (no undo) before continuing", len(lost), lost)
	}
	fmt.Fprintln(stderr, "next: pcb save → doc reload → pcb drc / pcb check (routing results read before a reload may be stale)")
	return nil
}
