package app

// pourIDsToReplace picks the pours `pour-fit --replace` clears: same net on the
// same layer. Matching on the net alone deleted the TOP GND pour when BOTTOM was
// poured next (live 2026-10-17: two pour-fit calls left one pour).
func pourIDsToReplace(pours []any, net string, layer int) []any {
	var ids []any
	for _, pi := range pours {
		pm, ok := pi.(map[string]any)
		if !ok || asString(pm["net"]) != net || int(asFloat(pm["layer"])) != layer {
			continue
		}
		if id := asString(pm["primitiveId"]); id != "" {
			ids = append(ids, id)
		}
	}
	return ids
}
