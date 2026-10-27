package health

import "testing"

func TestToleratedChatGPT403(t *testing.T) {
	rows := []Result{
		{Name: "bilibili", Status: Pass, HTTPCode: 200},
		{Name: "github", Status: Pass, HTTPCode: 200},
		{Name: "google", Status: Fail},
		{Name: "youtube", Status: Fail},
		{Name: "chatgpt", Status: Challenge, HTTPCode: 403},
	}
	if !Satisfies(rows) {
		t.Fatal("ChatGPT 403 must satisfy the chosen availability rule")
	}
	if !Accepted(rows[4]) {
		t.Fatal("ChatGPT 403 not accepted")
	}
	rows[4].HTTPCode = 401
	if Satisfies(rows) {
		t.Fatal("401 must stay unavailable")
	}
	rows[4].HTTPCode = 403
	rows[4].Status = Fail
	if Satisfies(rows) {
		t.Fatal("HTTP 403 with unexpected classification must not count")
	}
	rows[4].Status = Challenge
	rows[4].Name = "bilibili"
	if Accepted(rows[4]) {
		t.Fatal("other website 403 must not be accepted")
	}
}

func TestMatrixCoverageToleratesChatGPT403(t *testing.T) {
	output := EvaluateMatrix([]NodeResult{
		{Node: "first", Results: []Result{{Name: "bilibili", Status: Pass}, {Name: "github", Status: Pass}}},
		{Node: "second", Results: []Result{{Name: "chatgpt", Status: Challenge, HTTPCode: 403}}},
	})
	if !output.CoveragePassed || output.SingleNodePass {
		t.Fatalf("bad cross-node matrix: %+v", output)
	}
	if !output.Coverage["chatgpt"] {
		t.Fatal("403 not counted in allowed site coverage")
	}
}
