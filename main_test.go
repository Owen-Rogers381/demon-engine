package main

import (
	"strings"
	"testing"
)

func TestSanitizeHTMLPreservesSpacesAroundInlineTags(t *testing.T) {
	input := "Слово<b>жирное</b>слово"
	output := SanitizeHTML(input)
	if strings.Contains(output, "жирное</b>слово") {
		t.Fatalf("expected whitespace after closing tag, got %q", output)
	}
	if strings.Contains(output, "Слово<b>") {
		t.Fatalf("expected space before opening tag, got %q", output)
	}
	if !strings.Contains(output, "<b>жирное</b>") {
		t.Fatalf("expected bold tag preserved, got %q", output)
	}
}

func TestCleanHTMLForTelegraphPreservesSpacesAroundInlineTags(t *testing.T) {
	input := "Текст<i>курсив</i>текст"
	output := CleanHTMLForTelegraph(input)
	if strings.Contains(output, "курсив</i>текст") {
		t.Fatalf("expected whitespace after closing tag, got %q", output)
	}
	if strings.Contains(output, "Текст<i>") {
		t.Fatalf("expected space before opening tag, got %q", output)
	}
}

func TestBuildNewUserAdminAlertUsesFallbacks(t *testing.T) {
	alert := buildNewUserAdminAlert("Иван", "Петров", "", 12345)
	if !strings.Contains(alert, "Имя: Иван Петров") {
		t.Fatalf("expected name in alert, got %q", alert)
	}
	if !strings.Contains(alert, "Username: нет username") {
		t.Fatalf("expected fallback username in alert, got %q", alert)
	}
	if !strings.Contains(alert, "ID: 12345") {
		t.Fatalf("expected user id in alert, got %q", alert)
	}
}
