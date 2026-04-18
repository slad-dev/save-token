package intelligent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"agent-gateway/internal/auth"
	"agent-gateway/internal/config"
	"agent-gateway/internal/policy"
	airuntime "agent-gateway/internal/runtime"
	"agent-gateway/internal/store"
)

const aggressiveConciseInstruction = "Respond strictly and concisely. Do not explain code unless asked. No pleasantries."

var (
	excessBlankLinePattern         = regexp.MustCompile(`\n{3,}`)
	leadingFillerPattern           = regexp.MustCompile(`(?i)^\s*(请问(一下)?|麻烦(你)?|能不能|可以(帮我)?|帮我|非常感谢|谢谢(你)?|劳烦|请帮忙|请你)\s*`)
	jsonFencePattern               = regexp.MustCompile("(?s)```(?:json)?\\s*\\n(.*?)\\n```")
	xmlFencePattern                = regexp.MustCompile("(?s)```(?:xml|html)?\\s*\\n(.*?)\\n```")
	codeFencePattern               = regexp.MustCompile("(?s)```([a-zA-Z0-9_+-]*)\\s*\\n(.*?)\\n```")
	openClawMetadataHeadingPattern = regexp.MustCompile(`(?i)^(conversation info|sender|thread starter|replied message|forwarded message context|chat history since last reply|untrusted context)\b.*:$`)
)

type Preprocessor struct {
	cfg             config.IntelligentConfig
	searcher        WebSearcher
	engine          *policy.Engine
	runtimeSettings RuntimeSettingsProvider
}

type RuntimeSettingsProvider interface {
	CompressionEnabled() bool
	AggressiveCompressionEnabled() bool
	BasicSlimmingEnabled() bool
	AggressiveSlimmingEnabled() bool
	OutputConstraintEnabled() bool
}

type WebSearcher interface {
	Search(ctx context.Context, query string) ([]SearchResult, error)
}

type SearchResult struct {
	Title   string
	URL     string
	Content string
}

type InputTooLargeError struct {
	CharacterCount  int
	CharacterLimit  int
	EstimatedTokens int
	TokenLimit      int
}

func (e *InputTooLargeError) Error() string {
	if e.TokenLimit > 0 {
		return fmt.Sprintf(
			"input is too large (%d characters, ~%d tokens). It exceeds configured limits (%d chars, %d tokens) and should be routed to a lightweight RAG flow",
			e.CharacterCount,
			e.EstimatedTokens,
			e.CharacterLimit,
			e.TokenLimit,
		)
	}
	return fmt.Sprintf(
		"input is too large (%d characters). It exceeds the configured limit of %d and should be routed to a lightweight RAG flow",
		e.CharacterCount,
		e.CharacterLimit,
	)
}

type ChatRequest struct {
	Model       string                     `json:"model"`
	Stream      bool                       `json:"stream,omitempty"`
	MaxTokens   *int                       `json:"max_tokens,omitempty"`
	Messages    []ChatMessage              `json:"messages"`
	ExtraFields map[string]json.RawMessage `json:"-"`
}

type ChatMessage struct {
	Role        string                     `json:"role"`
	Content     any                        `json:"content"`
	ExtraFields map[string]json.RawMessage `json:"-"`
}

type PrepareResult struct {
	Body                 []byte
	Model                string
	OriginalModel        string
	QueryText            string
	UsedTooling          bool
	CompressionApplied   bool
	IntentClass          string
	IntentConfidence     float64
	IntentReasons        []string
	RouteTier            string
	DecisionReason       string
	OriginalInputChars   int
	EstimatedInputChars  int
	OriginalInputTokens  int
	EstimatedInputTokens int
	Stream               bool
}

func NewPreprocessor(cfg config.IntelligentConfig, searcher WebSearcher) *Preprocessor {
	return &Preprocessor{
		cfg:      cfg,
		searcher: searcher,
		engine:   policy.NewEngine(cfg.Routing),
	}
}

func (p *Preprocessor) SetRuntimeSettingsProvider(provider RuntimeSettingsProvider) {
	p.runtimeSettings = provider
}

func (r *ChatRequest) UnmarshalJSON(data []byte) error {
	type alias struct {
		Model     string        `json:"model"`
		Stream    bool          `json:"stream,omitempty"`
		MaxTokens *int          `json:"max_tokens,omitempty"`
		Messages  []ChatMessage `json:"messages"`
	}

	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	delete(raw, "model")
	delete(raw, "stream")
	delete(raw, "max_tokens")
	delete(raw, "messages")

	r.Model = decoded.Model
	r.Stream = decoded.Stream
	r.MaxTokens = decoded.MaxTokens
	r.Messages = decoded.Messages
	r.ExtraFields = raw
	return nil
}

func (r ChatRequest) MarshalJSON() ([]byte, error) {
	payload := make(map[string]any, len(r.ExtraFields)+4)
	for key, value := range r.ExtraFields {
		payload[key] = value
	}
	payload["model"] = r.Model
	payload["messages"] = r.Messages
	if r.Stream {
		payload["stream"] = r.Stream
	}
	if r.MaxTokens != nil {
		payload["max_tokens"] = *r.MaxTokens
	}
	return json.Marshal(payload)
}

func (m *ChatMessage) UnmarshalJSON(data []byte) error {
	type alias struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	}

	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	delete(raw, "role")
	delete(raw, "content")

	m.Role = decoded.Role
	m.Content = decoded.Content
	m.ExtraFields = raw
	return nil
}

func (m ChatMessage) MarshalJSON() ([]byte, error) {
	payload := make(map[string]any, len(m.ExtraFields)+2)
	for key, value := range m.ExtraFields {
		payload[key] = value
	}
	payload["role"] = m.Role
	payload["content"] = m.Content
	return json.Marshal(payload)
}

func (r *ChatRequest) HasAnyTokenLimit() bool {
	if r == nil {
		return false
	}
	if r.MaxTokens != nil {
		return true
	}
	if _, ok := r.ExtraFields["max_completion_tokens"]; ok {
		return true
	}
	if _, ok := r.ExtraFields["max_output_tokens"]; ok {
		return true
	}
	return false
}

func (p *Preprocessor) PrepareChatRequest(ctx context.Context, body []byte) (*PrepareResult, error) {
	var request ChatRequest
	if err := json.Unmarshal(body, &request); err != nil {
		return nil, err
	}
	if request.Model == "" {
		return nil, fmt.Errorf("model is required")
	}

	request.Messages = stabilizePromptPrefix(request.Messages)
	preserveConversationShape := shouldPreserveConversationShape(request.Messages)

	principal, _ := auth.PrincipalFromContext(ctx)
	allowCompression := p.cfg.RAG.Enabled
	runtimeAggressiveCompression := false
	basicSlimmingEnabled := false
	aggressiveSlimmingEnabled := false
	outputConstraintEnabled := false
	keepRecentMessages := p.cfg.RAG.KeepRecentMessages
	if p.runtimeSettings != nil {
		allowCompression = p.runtimeSettings.CompressionEnabled()
		runtimeAggressiveCompression = p.runtimeSettings.AggressiveCompressionEnabled()
		basicSlimmingEnabled = p.runtimeSettings.BasicSlimmingEnabled()
		aggressiveSlimmingEnabled = p.runtimeSettings.AggressiveSlimmingEnabled()
		outputConstraintEnabled = p.runtimeSettings.OutputConstraintEnabled()
	}
	if principal != nil && principal.APIKey.Project != nil && principal.APIKey.Project.AggressiveCompression {
		runtimeAggressiveCompression = true
	}
	if runtimeAggressiveCompression {
		keepRecentMessages = minInt(maxInt(p.cfg.RAG.KeepRecentMessages/2, 4), p.cfg.RAG.KeepRecentMessages)
	}

	originalCharCount := totalCharacterCount(request.Messages)
	originalTokenCount := estimateTokenCount(originalCharCount)
	charCount := originalCharCount
	tokenCount := originalTokenCount
	compressionApplied := false
	decisionReason := []string{"stable_prompt_prefix"}
	if preserveConversationShape {
		decisionReason = append(decisionReason, "preserve_structured_messages")
	}

	if basicSlimmingEnabled && !preserveConversationShape {
		var slimmed bool
		request.Messages, slimmed = slimMessages(request.Messages, aggressiveSlimmingEnabled)
		if slimmed {
			compressionApplied = true
			decisionReason = append(decisionReason, "prompt_slimming")
			charCount = totalCharacterCount(request.Messages)
			tokenCount = estimateTokenCount(charCount)
		}
	}

	shouldCompress := allowCompression && !preserveConversationShape && (charCount > p.cfg.RAG.CompressionThresholdChars || tokenCount > p.cfg.RAG.MaxEstimatedTokens)
	if allowCompression && !preserveConversationShape && runtimeAggressiveCompression && len(request.Messages) > keepRecentMessages+3 && charCount > 800 {
		shouldCompress = true
	}
	if shouldCompress {
		compressedMessages, applied := compressMessages(request.Messages, compressionPlan{
			KeepRecentMessages: keepRecentMessages,
			KeepFirstTurns:     2,
		})
		if applied {
			request.Messages = compressedMessages
			compressionApplied = true
			decisionReason = append(decisionReason, "sliding_window_trim")
			charCount = totalCharacterCount(request.Messages)
			tokenCount = estimateTokenCount(charCount)
		}
	}

	if allowCompression && !preserveConversationShape && exceedsInputBudget(charCount, tokenCount, p.cfg.RAG) {
		compressedMessages, applied := forceCompressMessages(request.Messages, compressionPlan{
			KeepRecentMessages: minInt(4, maxInt(keepRecentMessages/2, 2)),
			KeepFirstTurns:     1,
		})
		if applied {
			request.Messages = compressedMessages
			compressionApplied = true
			decisionReason = append(decisionReason, "forced_window_trim")
			charCount = totalCharacterCount(request.Messages)
			tokenCount = estimateTokenCount(charCount)
		}
	}

	queryText := latestUserText(request.Messages)
	if queryText != "" {
		decisionReason = append(decisionReason, "semantic_query="+quoteDecisionValue(summarizeForDecision(queryText, 96)))
	}
	if outputConstraintEnabled {
		request.Messages = injectSystemContext(request.Messages, aggressiveConciseInstruction)
		decisionReason = append(decisionReason, "concise_output_instruction")
		charCount = totalCharacterCount(request.Messages)
		tokenCount = estimateTokenCount(charCount)
		if shouldDefaultMaxTokens(queryText, request.Messages) && !request.HasAnyTokenLimit() {
			limit := 300
			request.MaxTokens = &limit
			decisionReason = append(decisionReason, "default_max_tokens=300")
		}
	}

	if exceedsInputBudget(charCount, tokenCount, p.cfg.RAG) {
		return nil, &InputTooLargeError{
			CharacterCount:  charCount,
			CharacterLimit:  p.cfg.RAG.MaxInputCharacters,
			EstimatedTokens: tokenCount,
			TokenLimit:      p.cfg.RAG.MaxEstimatedTokens,
		}
	}

	if queryText == "" {
		updatedBody, err := json.Marshal(request)
		if err != nil {
			return nil, err
		}
		return &PrepareResult{
			Body:                 updatedBody,
			Model:                request.Model,
			OriginalModel:        request.Model,
			CompressionApplied:   compressionApplied,
			DecisionReason:       strings.Join(decisionReason, "; "),
			OriginalInputChars:   originalCharCount,
			EstimatedInputChars:  charCount,
			OriginalInputTokens:  originalTokenCount,
			EstimatedInputTokens: tokenCount,
			Stream:               request.Stream,
		}, nil
	}

	webSearchAllowed := p.cfg.WebSearch.Enabled && p.searcher != nil
	strictPrivacy := false
	userID := uint(0)
	apiKeyID := uint(0)
	if principal != nil {
		userID = principal.User.ID
		apiKeyID = principal.APIKey.ID
		if principal.APIKey.Project != nil && strings.EqualFold(strings.TrimSpace(principal.APIKey.Project.PrivacyMode), store.PrivacyModeStrict) {
			strictPrivacy = true
		}
		if !principal.APIKey.AllowWebSearch {
			webSearchAllowed = false
		}
		if principal.APIKey.Project != nil && !principal.APIKey.Project.WebSearchEnabled {
			webSearchAllowed = false
		}
		if strictPrivacy {
			webSearchAllowed = false
		}
	}

	intent := classifyIntent(queryText, charCount)
	requestContext := &airuntime.RequestContext{
		Endpoint:             "/v1/chat/completions",
		UserID:               userID,
		APIKeyID:             apiKeyID,
		OriginalModel:        request.Model,
		FinalModel:           request.Model,
		EstimatedInputChars:  charCount,
		EstimatedInputTokens: tokenCount,
		AllowWebSearch:       webSearchAllowed,
		AllowCache:           !strictPrivacy,
		AllowCompression:     allowCompression,
		IntentClass:          intent.Class,
		IntentConfidence:     intent.Confidence,
		IntentReasons:        append([]string(nil), intent.Reasons...),
	}

	decision := p.engine.Evaluate(requestContext)
	request.Model = decision.FinalModel
	if strictPrivacy {
		decision.Reason = appendReason(decision.Reason, "strict_privacy_mode")
	}

	usedTooling := false
	if !strictPrivacy && decision.EnableWebSearch && needsRealtimeWebSearch(queryText) {
		originalMessages := request.Messages
		keywords := extractKeywords(queryText)
		searchResults, err := p.searcher.Search(ctx, keywords)
		if err == nil && len(searchResults) > 0 {
			request.Messages = injectSystemContext(request.Messages, buildSearchContext(keywords, searchResults))
			usedTooling = true
			charCount = totalCharacterCount(request.Messages)
			tokenCount = estimateTokenCount(charCount)

			if allowCompression && exceedsInputBudget(charCount, tokenCount, p.cfg.RAG) {
				compressedMessages, applied := forceCompressMessages(request.Messages, compressionPlan{
					KeepRecentMessages: minInt(4, maxInt(keepRecentMessages/2, 2)),
					KeepFirstTurns:     1,
				})
				if applied {
					request.Messages = compressedMessages
					charCount = totalCharacterCount(request.Messages)
					tokenCount = estimateTokenCount(charCount)
					compressionApplied = true
					decisionReason = append(decisionReason, "search_context_trimmed")
				}
			}

			if exceedsInputBudget(charCount, tokenCount, p.cfg.RAG) {
				request.Messages = originalMessages
				usedTooling = false
				charCount = totalCharacterCount(request.Messages)
				tokenCount = estimateTokenCount(charCount)
				decisionReason = append(decisionReason, "web_search_context_dropped_due_to_budget")
			}
		}
	}

	decision.Reason = appendReason(decision.Reason, strings.Join(decisionReason, "; "))

	updatedBody, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}

	return &PrepareResult{
		Body:                 updatedBody,
		Model:                request.Model,
		OriginalModel:        requestContext.OriginalModel,
		QueryText:            queryText,
		UsedTooling:          usedTooling,
		CompressionApplied:   compressionApplied,
		IntentClass:          decision.IntentClass,
		IntentConfidence:     decision.IntentConfidence,
		IntentReasons:        append([]string(nil), decision.IntentReasons...),
		RouteTier:            decision.RouteTier,
		DecisionReason:       decision.Reason,
		OriginalInputChars:   originalCharCount,
		EstimatedInputChars:  charCount,
		OriginalInputTokens:  originalTokenCount,
		EstimatedInputTokens: tokenCount,
		Stream:               request.Stream,
	}, nil
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}

func minInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}

func estimateTokenCount(charCount int) int {
	if charCount <= 0 {
		return 0
	}
	estimated := (charCount + 3) / 4
	return estimated + 16
}

func exceedsInputBudget(charCount, tokenCount int, cfg config.RAGConfig) bool {
	return charCount > cfg.MaxInputCharacters || tokenCount > cfg.MaxEstimatedTokens
}

func totalCharacterCount(messages []ChatMessage) int {
	total := 0
	for _, message := range messages {
		total += contentCharacterCount(message.Content)
	}
	return total
}

func latestUserText(messages []ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.ToLower(strings.TrimSpace(messages[i].Role)) != "user" {
			continue
		}
		text := buildSemanticQueryText(contentToText(messages[i].Content))
		if text != "" {
			return text
		}
	}
	return ""
}

func contentCharacterCount(content any) int {
	return utf8.RuneCountInString(contentToText(content))
}

func contentToText(content any) string {
	switch value := content.(type) {
	case string:
		return value
	case []any:
		var builder strings.Builder
		for _, item := range value {
			object, ok := item.(map[string]any)
			if !ok {
				continue
			}
			itemType, _ := object["type"].(string)
			switch itemType {
			case "text", "input_text":
				text, _ := object["text"].(string)
				builder.WriteString(text)
				builder.WriteString("\n")
			}
		}
		return strings.TrimSpace(builder.String())
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return ""
		}
		return string(raw)
	}
}

func stabilizePromptPrefix(messages []ChatMessage) []ChatMessage {
	if len(messages) == 0 {
		return messages
	}
	normalized := make([]ChatMessage, 0, len(messages))
	for _, message := range messages {
		role := strings.ToLower(strings.TrimSpace(message.Role))
		message.Role = role
		normalized = append(normalized, message)
	}
	return normalized
}

func injectSystemContext(messages []ChatMessage, context string) []ChatMessage {
	context = strings.TrimSpace(context)
	if context == "" {
		return messages
	}

	for _, message := range messages {
		if strings.ToLower(strings.TrimSpace(message.Role)) != "system" {
			continue
		}
		if strings.TrimSpace(contentToText(message.Content)) == context {
			return messages
		}
	}

	systemMessage := ChatMessage{
		Role:    "system",
		Content: context,
	}

	insertAt := 0
	for insertAt < len(messages) && strings.ToLower(strings.TrimSpace(messages[insertAt].Role)) == "system" {
		insertAt++
	}

	updated := make([]ChatMessage, 0, len(messages)+1)
	updated = append(updated, messages[:insertAt]...)
	updated = append(updated, systemMessage)
	updated = append(updated, messages[insertAt:]...)
	return updated
}

func buildSearchContext(query string, results []SearchResult) string {
	var buffer bytes.Buffer
	buffer.WriteString("You are receiving supplemental real-time web context gathered by the gateway before this request.\n")
	buffer.WriteString("Use it only when relevant, and prefer the most recent or source-backed facts.\n")
	buffer.WriteString("If the search evidence is incomplete or conflicting, say so clearly.\n")
	buffer.WriteString("Search query: ")
	buffer.WriteString(query)
	buffer.WriteString("\n\nSearch results:\n")

	for index, result := range results {
		title := sanitizeSearchText(result.Title, 160)
		if title == "" {
			title = "Untitled result"
		}
		snippet := sanitizeSearchText(result.Content, 800)
		if snippet == "" {
			continue
		}

		buffer.WriteString(fmt.Sprintf("[%d] %s\n", index+1, title))
		buffer.WriteString("URL: ")
		buffer.WriteString(result.URL)
		buffer.WriteString("\n")
		buffer.WriteString("Snippet: ")
		buffer.WriteString(snippet)
		buffer.WriteString("\n\n")
	}

	return strings.TrimSpace(buffer.String())
}

func shouldDefaultMaxTokens(queryText string, messages []ChatMessage) bool {
	if utf8.RuneCountInString(strings.TrimSpace(queryText)) > 280 {
		return false
	}

	userTurns := 0
	for _, message := range messages {
		if strings.ToLower(strings.TrimSpace(message.Role)) == "user" {
			userTurns++
		}
	}
	return userTurns <= 2
}

func appendReason(base, extra string) string {
	base = strings.TrimSpace(base)
	extra = strings.TrimSpace(extra)
	switch {
	case base == "":
		return extra
	case extra == "":
		return base
	default:
		return base + "; " + extra
	}
}

func summarizeForDecision(input string, limit int) string {
	input = strings.Join(strings.Fields(strings.TrimSpace(input)), " ")
	if input == "" || limit <= 0 {
		return input
	}
	runes := []rune(input)
	if len(runes) <= limit {
		return input
	}
	return strings.TrimSpace(string(runes[:limit])) + "..."
}

func quoteDecisionValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	value = strings.ReplaceAll(value, `"`, `'`)
	return `"` + value + `"`
}

func slimMessages(messages []ChatMessage, aggressive bool) ([]ChatMessage, bool) {
	updated := make([]ChatMessage, len(messages))
	changed := false
	for i, message := range messages {
		nextContent, contentChanged := slimContent(message.Content, aggressive, strings.ToLower(strings.TrimSpace(message.Role)))
		message.Content = nextContent
		updated[i] = message
		changed = changed || contentChanged
	}
	return updated, changed
}

func slimContent(content any, aggressive bool, role string) (any, bool) {
	switch value := content.(type) {
	case string:
		slimmed := slimText(value, aggressive, role)
		return slimmed, slimmed != value
	case []any:
		updated := make([]any, len(value))
		changed := false
		for i, item := range value {
			object, ok := item.(map[string]any)
			if !ok {
				updated[i] = item
				continue
			}
			cloned := make(map[string]any, len(object))
			for key, field := range object {
				cloned[key] = field
			}
			itemType, _ := cloned["type"].(string)
			if itemType == "text" || itemType == "input_text" {
				text, _ := cloned["text"].(string)
				slimmed := slimText(text, aggressive, role)
				if slimmed != text {
					cloned["text"] = slimmed
					changed = true
				}
			}
			updated[i] = cloned
		}
		return updated, changed
	default:
		return content, false
	}
}

func slimText(input string, aggressive bool, role string) string {
	normalized := strings.ReplaceAll(input, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	normalized = trimLineRightSpace(normalized)
	normalized = excessBlankLinePattern.ReplaceAllString(normalized, "\n\n")

	trimmed := strings.TrimSpace(normalized)
	if minified, ok := minifyJSON(trimmed); ok {
		return minified
	}
	if minified, ok := minifyXML(trimmed); ok {
		return minified
	}

	normalized = slimFencedJSON(normalized)
	if aggressive {
		normalized = slimFencedXML(normalized)
		normalized = slimFencedCodeComments(normalized)
		if role == "user" {
			normalized = leadingFillerPattern.ReplaceAllString(normalized, "")
		}
	}

	return strings.TrimSpace(normalized)
}

func trimLineRightSpace(input string) string {
	lines := strings.Split(input, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], " \t")
	}
	return strings.Join(lines, "\n")
}

func slimFencedJSON(input string) string {
	return jsonFencePattern.ReplaceAllStringFunc(input, func(block string) string {
		matches := jsonFencePattern.FindStringSubmatch(block)
		if len(matches) != 2 {
			return block
		}
		minified, ok := minifyJSON(strings.TrimSpace(matches[1]))
		if !ok {
			return block
		}
		return "```json\n" + minified + "\n```"
	})
}

func slimFencedXML(input string) string {
	return xmlFencePattern.ReplaceAllStringFunc(input, func(block string) string {
		matches := xmlFencePattern.FindStringSubmatch(block)
		if len(matches) != 2 {
			return block
		}
		minified, ok := minifyXML(strings.TrimSpace(matches[1]))
		if !ok {
			return block
		}
		return "```xml\n" + minified + "\n```"
	})
}

func slimFencedCodeComments(input string) string {
	return codeFencePattern.ReplaceAllStringFunc(input, func(block string) string {
		matches := codeFencePattern.FindStringSubmatch(block)
		if len(matches) != 3 {
			return block
		}

		language := strings.ToLower(strings.TrimSpace(matches[1]))
		if !supportsCommentTrimming(language) {
			return block
		}

		code := stripFullLineComments(matches[2], language)
		code = strings.TrimSpace(code)
		if code == "" {
			return block
		}
		return "```" + language + "\n" + code + "\n```"
	})
}

func supportsCommentTrimming(language string) bool {
	switch language {
	case "go", "js", "jsx", "ts", "tsx", "java", "c", "cpp", "cxx", "cs", "py", "python", "rb", "php", "sh", "bash", "zsh", "sql":
		return true
	default:
		return false
	}
}

func stripFullLineComments(input, language string) string {
	input = regexp.MustCompile(`(?s)/\*.*?\*/`).ReplaceAllString(input, "")
	input = regexp.MustCompile(`(?s)<!--.*?-->`).ReplaceAllString(input, "")

	lines := strings.Split(input, "\n")
	filtered := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			filtered = append(filtered, "")
			continue
		}
		if isCommentLine(trimmed, language) {
			continue
		}
		filtered = append(filtered, strings.TrimRight(line, " \t"))
	}
	return excessBlankLinePattern.ReplaceAllString(strings.Join(filtered, "\n"), "\n\n")
}

func isCommentLine(line, language string) bool {
	switch language {
	case "py", "python", "rb", "sh", "bash", "zsh":
		return strings.HasPrefix(line, "#")
	case "sql":
		return strings.HasPrefix(line, "--")
	default:
		return strings.HasPrefix(line, "//")
	}
}

func minifyJSON(input string) (string, bool) {
	if input == "" {
		return "", false
	}
	if !(strings.HasPrefix(input, "{") || strings.HasPrefix(input, "[")) {
		return "", false
	}
	var decoded any
	if err := json.Unmarshal([]byte(input), &decoded); err != nil {
		return "", false
	}
	encoded, err := json.Marshal(decoded)
	if err != nil {
		return "", false
	}
	return string(encoded), true
}

func minifyXML(input string) (string, bool) {
	if input == "" || !strings.HasPrefix(input, "<") || !strings.HasSuffix(input, ">") {
		return "", false
	}
	compacted := strings.Join(strings.Fields(input), " ")
	compacted = strings.ReplaceAll(compacted, "> <", "><")
	if compacted == "" {
		return "", false
	}
	return compacted, true
}

func buildSemanticQueryText(input string) string {
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	input = stripOpenClawMetadataSections(input)
	input = codeFencePattern.ReplaceAllString(input, " ")
	input = jsonFencePattern.ReplaceAllString(input, " ")
	input = xmlFencePattern.ReplaceAllString(input, " ")
	input = excessBlankLinePattern.ReplaceAllString(input, "\n\n")

	lines := strings.Split(input, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if looksStructuredLine(line) {
			continue
		}
		kept = append(kept, line)
		if len(kept) >= 4 {
			break
		}
	}

	normalized := strings.Join(kept, " ")
	normalized = strings.Join(strings.Fields(normalized), " ")
	normalized = strings.TrimSpace(normalized)
	if utf8.RuneCountInString(normalized) > 240 {
		runes := []rune(normalized)
		normalized = strings.TrimSpace(string(runes[:240]))
	}
	return normalized
}

func looksStructuredLine(line string) bool {
	if line == "" {
		return false
	}
	if strings.HasPrefix(line, "```") {
		return true
	}
	if strings.HasPrefix(line, "{") || strings.HasPrefix(line, "}") ||
		strings.HasPrefix(line, "[") || strings.HasPrefix(line, "]") ||
		strings.HasPrefix(line, "<") || strings.HasPrefix(line, ">") {
		return true
	}

	colonCount := strings.Count(line, ":")
	commaCount := strings.Count(line, ",")
	quoteCount := strings.Count(line, "\"")
	return colonCount >= 2 || commaCount >= 3 || quoteCount >= 4
}

func shouldPreserveConversationShape(messages []ChatMessage) bool {
	for _, message := range messages {
		if len(message.ExtraFields) > 0 {
			return true
		}
		if strings.EqualFold(strings.TrimSpace(message.Role), "tool") {
			return true
		}
		if containsOpenClawMetadata(contentToText(message.Content)) {
			return true
		}
	}
	return false
}

func containsOpenClawMetadata(input string) bool {
	normalized := strings.ReplaceAll(input, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	for _, line := range strings.Split(normalized, "\n") {
		if openClawMetadataHeadingPattern.MatchString(strings.TrimSpace(line)) {
			return true
		}
	}
	return false
}

func stripOpenClawMetadataSections(input string) string {
	lines := strings.Split(input, "\n")
	filtered := make([]string, 0, len(lines))

	for i := 0; i < len(lines); i++ {
		rawLine := lines[i]
		line := strings.TrimSpace(rawLine)
		if openClawMetadataHeadingPattern.MatchString(line) {
			for i+1 < len(lines) && strings.TrimSpace(lines[i+1]) == "" {
				i++
			}
			if i+1 < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i+1]), "```") {
				i += 2
				for i < len(lines) && !strings.HasPrefix(strings.TrimSpace(lines[i]), "```") {
					i++
				}
			}
			continue
		}
		filtered = append(filtered, rawLine)
	}

	return strings.TrimSpace(strings.Join(filtered, "\n"))
}
