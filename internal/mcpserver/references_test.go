package mcpserver

import (
	"strings"
	"testing"
)

// A variable read from a secret manager is listed with where it is read
// from, and its value is not passed on — even by a panel that sent one under a
// variable not marked secret. Setting one sends the reference and no value.
func TestAVariableFromASecretManagerIsShownByWhereItComesFrom(t *testing.T) {
	panel := newFakePanel(t)
	session := connect(t, New(panel.config()))

	text, failed := call(t, session, "list_variables", map[string]any{"app_id": "app_1"})
	if failed || !strings.Contains(text, "company-vault:shop#stripe_key") || strings.Contains(text, plantedReference) {
		t.Errorf("list_variables answered (error=%v): %s", failed, text)
	}

	text, failed = call(t, session, "set_variable", map[string]any{
		"app_id": "app_1", "key": "STRIPE_KEY", "value": "", "from": "company-vault:shop#stripe_key",
	})
	if failed {
		t.Fatalf("set_variable with from failed: %s", text)
	}
	request, ok := panel.last("PUT", "/api/apps/app_1/variables")
	if !ok {
		t.Fatal("set_variable did not reach the panel")
	}
	from, _ := request.Body["from"].(map[string]any)
	if from["connection"] != "company-vault" || from["path"] != "shop" || from["key"] != "stripe_key" {
		t.Errorf("set_variable sent %v", request.Body)
	}
	if _, sent := request.Body["value"]; sent {
		t.Errorf("set_variable sent a value beside the reference: %v", request.Body)
	}

	text, failed = call(t, session, "set_variable", map[string]any{"app_id": "app_1", "key": "X", "value": "", "from": "no-colon"})
	if !failed || !strings.Contains(text, "not a reference") {
		t.Errorf("a malformed reference was not refused before the panel was asked: %s", text)
	}

	text, failed = call(t, session, "refresh_variables", map[string]any{"app_id": "app_1"})
	if failed || !strings.Contains(text, "STRIPE_KEY") || strings.Contains(text, plantedReference) {
		t.Errorf("refresh_variables answered (error=%v): %s", failed, text)
	}
}
