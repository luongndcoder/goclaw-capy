package mcp

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseVerifyWrites(t *testing.T) {
	settings := json.RawMessage(`{"verify_writes": {
		"update_cells": {"tool": "get_sheet_data",
			"args": {"spreadsheet_id": "$spreadsheet_id", "sheet": "$sheet", "range": "$range"},
			"max_chars": 2000},
		"append_rows": {"tool": "get_sheet_data",
			"args": {"spreadsheet_id": "$spreadsheet_id", "sheet": "$sheet"}}
	}}`)
	got := ParseVerifyWrites(settings)
	if len(got) != 2 {
		t.Fatalf("expected 2 specs, got %d", len(got))
	}
	u := got["update_cells"]
	if u.Tool != "get_sheet_data" {
		t.Errorf("expected tool=get_sheet_data, got %q", u.Tool)
	}
	if u.Args["range"] != "$range" {
		t.Errorf("expected range=$range, got %q", u.Args["range"])
	}
	if u.maxChars() != 2000 {
		t.Errorf("expected maxChars=2000, got %d", u.maxChars())
	}
	// default when unset
	if got["append_rows"].maxChars() != DefaultVerifyMaxChars {
		t.Errorf("expected default maxChars, got %d", got["append_rows"].maxChars())
	}
	// cap enforced
	big := VerifySpec{Tool: "x", MaxChars: 999999}
	if big.maxChars() != MaxVerifyMaxChars {
		t.Errorf("expected cap %d, got %d", MaxVerifyMaxChars, big.maxChars())
	}
}

func TestParseVerifyWrites_Invalid(t *testing.T) {
	if got := ParseVerifyWrites(nil); got != nil {
		t.Errorf("expected nil for empty settings, got %v", got)
	}
	if got := ParseVerifyWrites(json.RawMessage(`{oops`)); got != nil {
		t.Errorf("expected nil for malformed JSON, got %v", got)
	}
	if got := ParseVerifyWrites(json.RawMessage(`{"tool_hints": {}}`)); got != nil {
		t.Errorf("expected nil when key absent, got %v", got)
	}
	// empty tool name and self-reference are dropped
	got := ParseVerifyWrites(json.RawMessage(`{"verify_writes": {
		"": {"tool": "x", "args": {}},
		"self": {"tool": "self", "args": {}},
		"ok": {"tool": "read", "args": {}}
	}}`))
	if len(got) != 1 || got["ok"].Tool != "read" {
		t.Errorf("expected only the valid spec, got %v", got)
	}
}

func TestResolveVerifyArgs(t *testing.T) {
	spec := VerifySpec{Tool: "get_sheet_data", Args: map[string]string{
		"spreadsheet_id": "$spreadsheet_id",
		"sheet":          "$sheet",
		"range":          "$range",
		"fixed":          "Sheet1",
	}}
	src := map[string]any{"spreadsheet_id": "abc", "sheet": "S1", "range": "A1:B2"}
	got, missing := resolveVerifyArgs(spec, src)
	if len(missing) != 0 {
		t.Errorf("expected no missing, got %v", missing)
	}
	if got["spreadsheet_id"] != "abc" || got["sheet"] != "S1" || got["range"] != "A1:B2" {
		t.Errorf("bad substitution: %v", got)
	}
	if got["fixed"] != "Sheet1" {
		t.Errorf("literal passthrough broken: %v", got)
	}

	// missing + non-string refs are reported, not fabricated
	src2 := map[string]any{"spreadsheet_id": "abc", "count": 3}
	spec2 := VerifySpec{Tool: "get_sheet_data", Args: map[string]string{
		"spreadsheet_id": "$spreadsheet_id",
		"sheet":          "$sheet",
		"count":          "$count",
	}}
	got2, missing2 := resolveVerifyArgs(spec2, src2)
	if _, ok := got2["sheet"]; ok {
		t.Errorf("missing ref must be omitted, got %v", got2)
	}
	if _, ok := got2["count"]; ok {
		t.Errorf("non-string ref must be omitted, got %v", got2)
	}
	if len(missing2) != 2 {
		t.Errorf("expected 2 missing, got %v", missing2)
	}
}

func TestFormatVerifySection(t *testing.T) {
	out := formatVerifySection("get_sheet_data", "A1", 2000)
	if !strings.Contains(out, "get_sheet_data") || !strings.Contains(out, "A1") {
		t.Errorf("section must name the tool and include data: %q", out)
	}
	long := strings.Repeat("x", 3000)
	out = formatVerifySection("get_sheet_data", long, 2000)
	if len(out) > 2000+400 {
		t.Errorf("section must truncate, len=%d", len(out))
	}
	if !strings.Contains(out, "truncated") {
		t.Errorf("truncation must be marked: %q", out[len(out)-100:])
	}
}

func TestFormatVerifyFailure(t *testing.T) {
	out := formatVerifyFailure("get_sheet_data", errVerifyNoClient)
	for _, want := range []string{"get_sheet_data", "NOT", "confirmed", "manually"} {
		if !strings.Contains(out, want) {
			t.Errorf("failure note must contain %q: %q", want, out)
		}
	}
}
