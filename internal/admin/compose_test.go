package admin

import (
	"fmt"
	"strings"
	"testing"
)

func TestParseRecipients(t *testing.T) {
	got, err := parseRecipients([]string{" ada@example.com ", "Grace <grace@example.com>", "ADA@example.com", ""})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "ada@example.com,grace@example.com" {
		t.Fatalf("expected trimmed, de-duplicated addresses, got %v", got)
	}

	if _, err := parseRecipients([]string{"ok@example.com", "not an email"}); err == nil || !strings.Contains(err.Error(), "not an email") {
		t.Fatalf("expected invalid address error, got %v", err)
	}
	if _, err := parseRecipients([]string{" ", ""}); err == nil {
		t.Fatal("expected error for no recipients")
	}

	many := make([]string, maxComposeRecipients+1)
	for i := range many {
		many[i] = fmt.Sprintf("user%d@example.com", i)
	}
	if _, err := parseRecipients(many); err == nil {
		t.Fatal("expected error above the recipient cap")
	}
	if got, err := parseRecipients(many[:maxComposeRecipients]); err != nil || len(got) != maxComposeRecipients {
		t.Fatalf("expected exactly the cap to be allowed, got %d, %v", len(got), err)
	}
}
