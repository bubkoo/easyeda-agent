package app

import (
	"reflect"
	"testing"
)

func TestBuildOfficialAutoroutePayload(t *testing.T) {
	got, err := buildOfficialAutoroutePayload(nil, []string{"GND", "XTAL_P"}, []int{1, 2}, "45", "completion")
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"cornerStyle": "45", "optimization": "completion", "ignoreNets": []string{"GND", "XTAL_P"}, "layers": []int{1, 2}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("payload=%v want %v", got, want)
	}
	if _, err := buildOfficialAutoroutePayload(nil, nil, nil, "30", "completion"); err == nil {
		t.Fatal("corner 30 accepted")
	}
	if _, err := buildOfficialAutoroutePayload(nil, nil, nil, "45", "best"); err == nil {
		t.Fatal("optimize best accepted")
	}
}
