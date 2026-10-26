package handler

import (
	"strings"
	"testing"
)

func TestParseRequestError(t *testing.T) {
	long := strings.Repeat("x", maxParseTextRunes+1)
	lists := make([]string, maxParseLists+1)
	cases := []struct {
		name string
		req  parseTaskRequest
		want string
	}{
		{"ok", parseTaskRequest{Text: "buy milk", ListNames: []string{"Home"}}, ""},
		{"empty text", parseTaskRequest{}, "text is required"},
		{"max text ok", parseTaskRequest{Text: strings.Repeat("x", maxParseTextRunes)}, ""},
		{"text too long", parseTaskRequest{Text: long}, "text too long"},
		{"multibyte counted as runes", parseTaskRequest{Text: strings.Repeat("好", maxParseTextRunes)}, ""},
		{"too many lists", parseTaskRequest{Text: "a", ListNames: lists}, "too many list names"},
		{"list name too long", parseTaskRequest{Text: "a", ListNames: []string{strings.Repeat("y", maxParseListRunes+1)}}, "list name too long"},
	}
	for _, tc := range cases {
		if got := parseRequestError(tc.req); got != tc.want {
			t.Errorf("%s: got %q, want %q", tc.name, got, tc.want)
		}
	}
}
