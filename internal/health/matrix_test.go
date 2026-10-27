package health

import "testing"

func TestMatrixCoverageAcrossNodes(t *testing.T) {
	a := NodeResult{Node: "one", Results: []Result{{Name: "bilibili", HTTPCode: 200}, {Name: "google", HTTPCode: 404}}}
	b := NodeResult{Node: "two", Results: []Result{{Name: "github", HTTPCode: 403}}}
	m := EvaluateMatrix([]NodeResult{a, b})
	if !m.CoveragePassed || m.SingleNodePass {
		t.Fatalf("unexpected coverage: %+v", m)
	}
}

func TestMatrixRequiresLoopback(t *testing.T) {
	if _, e := NewController("http://proxy.example.invalid:9090"); e == nil {
		t.Fatal("nonlocal controller accepted")
	}
}
