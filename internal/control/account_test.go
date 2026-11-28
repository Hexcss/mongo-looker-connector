package control

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestGeneratedAccount(t *testing.T) {
	a, p, err := NewAccount(" Editor@Example.com ", RoleEditor, false)
	if err != nil || a.Email != "editor@example.com" || len(a.ID) != 64 || !a.MustChangePassword || !a.PasswordMatches(p) {
		t.Fatal("account creation failed", err)
	}
	doc, _ := json.Marshal(a)
	if strings.Contains(string(doc), string(a.PasswordHash)) || strings.Contains(string(doc), "PasswordHash") || strings.Contains(string(doc), a.Revision) {
		t.Fatal("private fields in API response")
	}
	if a.PasswordMatches("wrong") {
		t.Fatal("accepted incorrect password")
	}
	if err := a.SetPassword(strings.Repeat("x", 73), false); err == nil {
		t.Fatal("accepted oversized password")
	}
	if _, _, err := NewAccount("display <editor@example.com>", RoleEditor, false); err == nil {
		t.Fatal("accepted display name")
	}
}
