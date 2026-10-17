package app

import (
	"reflect"
	"testing"
)

func TestPourIDsToReplaceKeepsOtherLayers(t *testing.T) {
	pours := []any{
		map[string]any{"primitiveId": "top", "net": "GND", "layer": float64(1)},
		map[string]any{"primitiveId": "bot", "net": "GND", "layer": float64(2)},
		map[string]any{"primitiveId": "v33", "net": "3V3", "layer": float64(2)},
	}
	if got := pourIDsToReplace(pours, "GND", 2); !reflect.DeepEqual(got, []any{"bot"}) {
		t.Fatalf("got %v, want only the BOTTOM GND pour", got)
	}
}
