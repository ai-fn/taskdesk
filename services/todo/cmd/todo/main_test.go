package main

import (
	"reflect"
	"testing"
)

func TestSplitList(t *testing.T) {
	got := splitList(" http://a ,, http://b,")
	if want := []string{"http://a", "http://b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("splitList = %q, want %q", got, want)
	}
}
