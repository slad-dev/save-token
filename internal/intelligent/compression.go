package intelligent

import "strings"

type compressionPlan struct {
	KeepRecentMessages int
	KeepFirstTurns     int
}

func compressMessages(messages []ChatMessage, plan compressionPlan) ([]ChatMessage, bool) {
	return trimConversationWindow(messages, plan)
}

func forceCompressMessages(messages []ChatMessage, plan compressionPlan) ([]ChatMessage, bool) {
	return trimConversationWindow(messages, plan)
}

func trimConversationWindow(messages []ChatMessage, plan compressionPlan) ([]ChatMessage, bool) {
	if len(messages) <= 2 {
		return messages, false
	}

	keepRecent := maxInt(plan.KeepRecentMessages, 2)
	keepFirstTurns := maxInt(plan.KeepFirstTurns, 1)

	systemPrefixCount := 0
	for systemPrefixCount < len(messages) && strings.EqualFold(strings.TrimSpace(messages[systemPrefixCount].Role), "system") {
		systemPrefixCount++
	}

	nonSystem := messages[systemPrefixCount:]
	if len(nonSystem) <= keepRecent+keepFirstTurns {
		return messages, false
	}

	firstEnd := minInt(keepFirstTurns, len(nonSystem))
	recentStart := len(nonSystem) - keepRecent
	if recentStart <= firstEnd {
		return messages, false
	}

	trimmed := make([]ChatMessage, 0, systemPrefixCount+firstEnd+keepRecent)
	trimmed = append(trimmed, messages[:systemPrefixCount]...)
	trimmed = append(trimmed, nonSystem[:firstEnd]...)
	trimmed = append(trimmed, nonSystem[recentStart:]...)
	return trimmed, len(trimmed) < len(messages)
}
