package app

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type reloadFixture struct {
	t                  *testing.T
	srv                *httptest.Server
	actions            []string
	openCount          int
	openTimeoutMs      int
	openErrorCode      string
	opened             bool
	closed             bool
	failReopen         bool
	failClose          bool
	noActiveAfterClose bool
	docType            string
	saveResult         map[string]any
	responseChange     func(string, map[string]any)
}

func newReloadFixture(t *testing.T, failReopen bool) (*reloadFixture, *appConfig) {
	t.Helper()
	fx := &reloadFixture{t: t, failReopen: failReopen, docType: "pcb", saveResult: map[string]any{"saved": true}}
	fx.srv = httptest.NewServer(http.HandlerFunc(fx.serveHTTP))
	t.Cleanup(fx.srv.Close)
	host, port, _ := strings.Cut(strings.TrimPrefix(fx.srv.URL, "http://"), ":")
	return fx, &appConfig{host: host, ports: port + "-" + port}
}

func (fx *reloadFixture) serveHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/health" {
		_, _ = w.Write([]byte(`{"service":"easyeda-agent","windows":[{"windowId":"w1"}]}`))
		return
	}
	var req struct {
		Action    string         `json:"action"`
		Payload   map[string]any `json:"payload"`
		TimeoutMs int            `json:"timeoutMs"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		fx.t.Error(err)
		return
	}
	fx.actions = append(fx.actions, req.Action)
	result := map[string]any{}
	ctx := map[string]any{
		"projectUuid": "project-1", "documentUuid": "pcb-target",
		"documentType": fx.docType, "tabId": "tab-target",
	}
	ok := true
	errorMessage := "fixture failure"
	errorCode := "EDA_CALL_FAILED"
	switch req.Action {
	case "schematic.pages.list":
		result["pages"] = []any{}
	case "pcb.documents.list":
		result["pcbs"] = []any{map[string]any{"uuid": "pcb-target", "name": "PCB1", "parentProjectUuid": "project-1"}}
	case "document.current":
		if fx.closed && !fx.opened {
			if fx.noActiveAfterClose {
				ok = false
				ctx = map[string]any{"projectUuid": "project-1"}
				errorMessage = "No active document."
			} else {
				ctx["documentUuid"], ctx["documentType"], ctx["tabId"] = "other-doc", "schematic", "tab-other"
			}
		}
	case "pcb.save", "schematic.save":
		result = fx.saveResult
	case "pcb.page.clear":
		result = map[string]any{"total": 0, "dryRun": req.Payload["dryRun"] == true}
	case "pcb.components.list", "schematic.components.list":
		result["count"] = 1
		result["components"] = []any{map[string]any{"primitiveId": "stable-part"}}
	case "document.close":
		if got := req.Payload["uuid"]; got != "pcb-target" {
			fx.t.Errorf("close uuid=%v", got)
		}
		if got := req.Payload["tabId"]; got != "tab-target" {
			fx.t.Errorf("close tabId=%v", got)
		}
		if fx.failClose {
			ok = false
			errorMessage = "host close failed"
			break
		}
		fx.closed = true
		ctx["documentUuid"], ctx["documentType"], ctx["tabId"] = "other-doc", "schematic", "tab-other"
		result = map[string]any{"closed": true, "uuid": "pcb-target", "tabId": "tab-target", "splitScreenId": "target-split"}
	case "document.open":
		fx.openCount++
		fx.openTimeoutMs = req.TimeoutMs
		if got := req.Payload["uuid"]; got != "pcb-target" {
			fx.t.Errorf("open uuid=%v", got)
		}
		if got := req.Payload["splitScreenId"]; got != "target-split" {
			fx.t.Errorf("open splitScreenId=%v, want preserved target-split", got)
		}
		if fx.failReopen {
			ok = false
			errorMessage = "host open failed"
			if fx.openErrorCode != "" {
				errorCode = fx.openErrorCode
			}
		} else {
			fx.opened = true
			result = map[string]any{"tabId": "tab-reopened", "ready": true}
		}
	default:
		fx.t.Errorf("unexpected action %s", req.Action)
	}
	if req.Action == "document.current" && ok {
		result = map[string]any{"uuid": ctx["documentUuid"], "documentType": ctx["documentType"], "tabId": ctx["tabId"], "parentProjectUuid": ctx["projectUuid"]}
	}
	response := map[string]any{"id": fmt.Sprintf("req-%d", len(fx.actions)), "windowId": "w1", "createdAt": time.Now().UTC().Format(time.RFC3339Nano), "ok": ok, "result": result, "context": ctx}
	if !ok {
		response["error"] = map[string]any{"code": errorCode, "message": errorMessage}
	}
	if fx.responseChange != nil {
		fx.responseChange(req.Action, response)
	}
	_ = json.NewEncoder(w).Encode(response)
}

func TestReloadDocumentTreatsNoActiveDocumentAsClosed(t *testing.T) {
	fx, cfg := newReloadFixture(t, false)
	fx.noActiveAfterClose = true
	if _, err := reloadDocumentByUUID(cfg, "w1", "pcb-target"); err != nil {
		t.Fatal(err)
	}
	if fx.openCount != 1 {
		t.Fatalf("document.open count=%d, want exactly one after the last tab closed", fx.openCount)
	}
}

func TestReloadDocumentPreservesTargetSplitAndWaitsForClose(t *testing.T) {
	fx, cfg := newReloadFixture(t, false)
	docType, err := reloadDocumentByUUID(cfg, "w1", "pcb-target")
	if err != nil {
		t.Fatal(err)
	}
	if docType != "pcb" {
		t.Fatalf("docType=%q", docType)
	}
	if fx.openCount != 1 {
		t.Fatalf("document.open count=%d, want exactly one", fx.openCount)
	}
	if fx.openTimeoutMs != 32_000 {
		t.Fatalf("document.open timeoutMs=%d, want bounded 32000 (30s connector budget plus grace)", fx.openTimeoutMs)
	}
	want := []string{"document.current", "pcb.save", "document.close", "document.current", "document.open", "document.current", "pcb.components.list", "document.current", "pcb.components.list", "document.current"}
	if strings.Join(fx.actions, ",") != strings.Join(want, ",") {
		t.Fatalf("actions=%v, want %v", fx.actions, want)
	}
}

func TestReloadDocumentCloseFailureDoesNotOpen(t *testing.T) {
	fx, cfg := newReloadFixture(t, false)
	fx.failClose = true
	_, err := reloadDocumentByUUID(cfg, "w1", "pcb-target")
	if err == nil || !strings.Contains(err.Error(), "close document failed") {
		t.Fatalf("error=%v", err)
	}
	if fx.openCount != 0 {
		t.Fatalf("document.open count=%d, want zero after failed close", fx.openCount)
	}
}

func TestReloadDocumentOpenFailureDoesNotRetryAndReportsRecovery(t *testing.T) {
	fx, cfg := newReloadFixture(t, true)
	_, err := reloadDocumentByUUID(cfg, "w1", "pcb-target")
	if err == nil {
		t.Fatal("expected reopen failure")
	}
	if fx.openCount != 1 {
		t.Fatalf("document.open count=%d, want exactly one", fx.openCount)
	}
	message := err.Error()
	for _, text := range []string{"target tab was closed", "reopen completion is unconfirmed", "automatic retry was suppressed", "retain this failure", "typed connection/context evidence"} {
		if !strings.Contains(message, text) {
			t.Fatalf("error missing %q: %s", text, message)
		}
	}
	for _, text := range []string{"project tree", "refreshing the editor"} {
		if strings.Contains(message, text) {
			t.Fatalf("error suggested a GUI recovery: %s", message)
		}
	}
}

func TestReloadDocumentUnconfirmedSaveDoesNotCloseOrReopen(t *testing.T) {
	for _, docType := range []string{"pcb", "schematic"} {
		for name, result := range map[string]map[string]any{
			"false":       {"saved": false},
			"missing":     {},
			"null":        {"saved": nil},
			"string true": {"saved": "true"},
		} {
			t.Run(docType+"/"+name, func(t *testing.T) {
				fx, cfg := newReloadFixture(t, false)
				fx.docType, fx.saveResult = docType, result
				gotType, err := reloadDocumentByUUID(cfg, "w1", "pcb-target")
				if err == nil || !strings.Contains(err.Error(), "saved:true required") || gotType != docType {
					t.Fatalf("type=%s err=%v", gotType, err)
				}
				if fx.closed || fx.openCount != 0 {
					t.Fatalf("unconfirmed save closed=%t openCount=%d", fx.closed, fx.openCount)
				}
				want := "document.current," + docType + ".save"
				if strings.Join(fx.actions, ",") != want {
					t.Fatalf("actions=%v, want only %s", fx.actions, want)
				}
			})
		}
	}
}

func TestReloadDocumentQueueBlockedDoesNotResubmitOpen(t *testing.T) {
	fx, cfg := newReloadFixture(t, true)
	fx.openErrorCode = "QUEUE_BLOCKED"
	_, err := reloadDocumentByUUID(cfg, "w1", "pcb-target")
	if err == nil || fx.openCount != 1 {
		t.Fatalf("queue-blocked reopen must fail after one request, opens=%d err=%v", fx.openCount, err)
	}
	if fx.actions[len(fx.actions)-1] != "document.open" {
		t.Fatalf("failed reopen must not claim recovery or issue further requests: %v", fx.actions)
	}
}
