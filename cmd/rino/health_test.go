package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestHealthCheckNeedsProxy(t *testing.T) {
	var out bytes.Buffer
	err := runHealth([]string{"check"}, &out)
	if err == nil {
		t.Fatal("health check accepted missing proxy")
	}
}

func TestHealthCheckFailureExitAndOutput(t *testing.T) {
	var out bytes.Buffer
	err := runHealth([]string{"check", "--proxy", "http://127.0.0.1:1", "--timeout", "250ms"}, &out)
	if err == nil {
		t.Fatal("unreachable proxy should fail condition")
	}
	if !strings.Contains(out.String(), "bilibili && github && (google || youtube || chatgpt): false") {
		t.Fatal("missing false acceptance report")
	}
}

func TestHealthCheckJSONFailureExit(t *testing.T) {
	var out bytes.Buffer
	err := runHealth([]string{"check", "--proxy", "http://127.0.0.1:1", "--timeout", "250ms", "--json"}, &out)
	if err == nil {
		t.Fatal("JSON probe should fail when all targets unreachable")
	}
	var obj struct {
		Passed  bool `json:"passed"`
		Results []struct {
			Name string `json:"name"`
		} `json:"results"`
	}
	if e := json.Unmarshal(out.Bytes(), &obj); e != nil {
		t.Fatal(e)
	}
	if obj.Passed || len(obj.Results) != 5 {
		t.Fatalf("wrong JSON status: %+v", obj)
	}
}
