package gateway

import "testing"

func TestPrioritizeCandidatesPrefersHealthyUpstreams(t *testing.T) {
	proxy := NewProxy(nil, nil, nil)
	proxy.markFailure("upstream-a")
	proxy.markFailure("upstream-a")

	candidates := []Upstream{
		{Name: "upstream-a"},
		{Name: "upstream-b"},
	}

	ordered := proxy.prioritizeCandidates(candidates)
	if len(ordered) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(ordered))
	}
	if ordered[0].Name != "upstream-b" {
		t.Fatalf("expected healthy upstream first, got %s", ordered[0].Name)
	}
}

func TestMarkSuccessClearsCooldownState(t *testing.T) {
	proxy := NewProxy(nil, nil, nil)
	proxy.markFailure("upstream-a")
	proxy.markFailure("upstream-a")
	if !proxy.isCoolingDown("upstream-a") {
		t.Fatalf("expected upstream-a to be cooling down after threshold failures")
	}

	proxy.markSuccess("upstream-a")
	if proxy.isCoolingDown("upstream-a") {
		t.Fatalf("expected upstream-a cooldown state to be cleared after success")
	}
}

func TestPrioritizeCandidatesKeepsOrderWhenAllCoolingDown(t *testing.T) {
	proxy := NewProxy(nil, nil, nil)
	proxy.markFailure("upstream-a")
	proxy.markFailure("upstream-a")
	proxy.markFailure("upstream-b")
	proxy.markFailure("upstream-b")

	candidates := []Upstream{
		{Name: "upstream-a"},
		{Name: "upstream-b"},
	}

	ordered := proxy.prioritizeCandidates(candidates)
	if len(ordered) != 2 {
		t.Fatalf("expected 2 candidates, got %d", len(ordered))
	}
	if ordered[0].Name != "upstream-a" || ordered[1].Name != "upstream-b" {
		t.Fatalf("expected original candidate order when all upstreams are cooling down")
	}
}
