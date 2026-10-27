package health

import "testing"

func TestAnyStatusIsReachableWhenReceivedFromTarget(t *testing.T) {
	for _, code := range []int{200, 204, 302, 401, 403, 404, 429, 500, 503} {
		for _, status := range []Status{Pass, Fail, Challenge, Limited} {
			result := Result{Name: "github", Status: status, HTTPCode: code}
			if !Accepted(result) {
				t.Fatalf("remote HTTP %d (%s) incorrectly rejected", code, status)
			}
		}
	}
	for _, code := range []int{0, -1, 99, 600} {
		if Accepted(Result{Name: "chatgpt", Status: Pass, HTTPCode: code}) {
			t.Fatalf("no valid target response (code %d) must fail", code)
		}
	}
}

func TestMatrixCoverageAcrossNodesUsesResponseEvidence(t *testing.T) {
	result := EvaluateMatrix([]NodeResult{
		{Node: "first", Passed: true, Results: []Result{{Name: "bilibili", HTTPCode: 503}, {Name: "github", HTTPCode: 401}}},
		{Node: "second", Results: []Result{{Name: "chatgpt", Status: Challenge, HTTPCode: 403}}},
	})
	if !result.CoveragePassed || result.SingleNodePass {
		t.Fatalf("incorrect overall connectivity: %+v", result)
	}
	if !result.Coverage["chatgpt"] {
		t.Fatal("HTTP 403 was not treated as reachable")
	}
}
