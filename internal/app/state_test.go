package app

import "testing"

func TestRuleSnapshotsDoNotShareMatchSlices(t *testing.T) {
	rules := []RuleView{{
		ID: "rule", Domains: []string{"example.com"}, IPs: []string{"10.0.0.0/8"},
		Process: []string{"browser"}, Networks: []string{"tcp"},
	}}
	s := NewState(Snapshot{})
	s.SetRules(rules)
	rules[0].Domains[0] = "input mutation"
	first := s.Snapshot()
	if first.Rules[0].Domains[0] != "example.com" {
		t.Fatal("SetRules retained caller-owned match values")
	}
	first.Rules[0].Domains[0] = "snapshot mutation"
	first.Rules[0].IPs[0] = "snapshot mutation"
	first.Rules[0].Process[0] = "snapshot mutation"
	first.Rules[0].Networks[0] = "snapshot mutation"
	second := s.Snapshot().Rules[0]
	if second.Domains[0] != "example.com" || second.IPs[0] != "10.0.0.0/8" ||
		second.Process[0] != "browser" || second.Networks[0] != "tcp" {
		t.Fatalf("snapshot mutation reached shared state: %+v", second)
	}
}
