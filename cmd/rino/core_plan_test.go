package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestCorePlanRejectsMissingArguments(t *testing.T) {
	for _, args := range [][]string{
		{"plan"},
		{"plan", "--current", "old.yaml"},
		{"plan", "--candidate", "new.yaml"},
		{"plan", "--current", "a", "--candidate", "b", "--timeout", "0s"},
		{"plan", "--current", "a", "--candidate", "b", "--timeout", "3m"},
		{"plan", "--current", "a", "--candidate", "b", "unexpected"},
	} {
		var out bytes.Buffer
		if err := runCore(args, &out); err == nil || out.Len() != 0 {
			t.Fatalf("args=%v err=%v out=%s", args, err, out.String())
		}
	}
}

func TestCorePlanIsReadOnly(t *testing.T) {
	var out bytes.Buffer
	err := runCore([]string{"plan", "--current", "a", "--candidate", "b", "--apply"}, &out)
	if err == nil || !strings.Contains(err.Error(), "flag") {
		t.Fatalf("unexpected apply support: %v", err)
	}
}
