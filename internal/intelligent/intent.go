package intelligent

import "strings"

type IntentDecision struct {
	Class      string
	Confidence float64
	Reasons    []string
}

func classifyIntent(input string, estimatedChars int) IntentDecision {
	normalized := strings.TrimSpace(strings.ToLower(input))
	if normalized == "" {
		return IntentDecision{
			Class:      "simple_chat",
			Confidence: 0.5,
			Reasons:    []string{"empty_or_short_input"},
		}
	}

	reasons := make([]string, 0, 3)
	intentClass := "simple_chat"
	confidence := 0.72

	if estimatedChars >= 8000 {
		intentClass = "long_context"
		confidence = 0.95
		reasons = append(reasons, "large_context_detected")
	}

	if containsComplexReasoningSignal(normalized) {
		intentClass = "complex_reasoning"
		confidence = 0.88
		reasons = append(reasons, "complex_reasoning_signal")
	}

	if needsRealtimeWebSearch(input) {
		intentClass = "realtime_lookup"
		confidence = 0.90
		reasons = append(reasons, "realtime_information_signal")
	}

	if len(reasons) == 0 {
		reasons = append(reasons, "default_simple_chat")
	}

	return IntentDecision{
		Class:      intentClass,
		Confidence: confidence,
		Reasons:    reasons,
	}
}

func containsComplexReasoningSignal(input string) bool {
	signals := []string{
		"step by step", "reason", "analyze", "compare", "tradeoff", "design", "architecture",
		"debug", "optimize", "algorithm", "prove", "derive", "plan",
		"一步一步", "分析", "推理", "比较", "权衡", "设计", "架构", "调试", "优化", "证明", "方案",
	}

	for _, signal := range signals {
		if strings.Contains(input, signal) {
			return true
		}
	}

	return false
}
