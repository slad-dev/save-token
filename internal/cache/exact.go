package cache

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"math"
	"sort"
	"strings"
	"sync"
	"time"

	"agent-gateway/internal/config"
	"agent-gateway/internal/store"
)

type ExactCache struct {
	cfg             config.CacheConfig
	store           *store.SQLiteStore
	runtimeSettings RuntimeSettingsProvider
	embedder        SemanticEmbedder
	embeddingMu     sync.RWMutex
	embeddingMemo   map[string]embeddingMemo
}

type SemanticEmbedder interface {
	Embed(ctx context.Context, texts []string) ([][]float64, error)
}

type embeddingMemo struct {
	vector    []float64
	expiresAt time.Time
}

type RuntimeSettingsProvider interface {
	SemanticCacheEnabled() bool
}

type CachedResponse struct {
	StatusCode  int
	ContentType string
	Body        []byte
}

func NewExactCache(cfg config.CacheConfig, st *store.SQLiteStore) *ExactCache {
	return &ExactCache{
		cfg:           cfg,
		store:         st,
		embeddingMemo: make(map[string]embeddingMemo),
	}
}

func (c *ExactCache) SetRuntimeSettingsProvider(provider RuntimeSettingsProvider) {
	c.runtimeSettings = provider
}

func (c *ExactCache) SetSemanticEmbedder(embedder SemanticEmbedder) {
	c.embedder = embedder
}

func (c *ExactCache) SemanticThreshold() float64 {
	if c == nil {
		return 0
	}
	return c.cfg.SemanticSimilarity
}

func (c *ExactCache) BuildRequestHash(body []byte) (string, error) {
	var normalized any
	if err := json.Unmarshal(body, &normalized); err != nil {
		return "", err
	}

	compact, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}

	sum := sha256.Sum256(compact)
	return hex.EncodeToString(sum[:]), nil
}

func (c *ExactCache) Lookup(ctx context.Context, userID uint, endpoint, requestHash string) (*CachedResponse, error) {
	if c == nil || c.store == nil || !c.cfg.Enabled {
		return nil, nil
	}

	entry, err := c.store.FindValidCacheEntry(ctx, userID, endpoint, requestHash)
	if err != nil || entry == nil {
		return nil, err
	}

	return &CachedResponse{
		StatusCode:  entry.StatusCode,
		ContentType: entry.ContentType,
		Body:        append([]byte(nil), entry.ResponseBody...),
	}, nil
}

func (c *ExactCache) LookupSemantic(ctx context.Context, userID uint, endpoint, model, queryText string) (*CachedResponse, float64, error) {
	if c == nil || c.store == nil || !c.cfg.Enabled || !c.semanticEnabled() {
		return nil, 0, nil
	}

	queryText = strings.TrimSpace(queryText)
	normalized := normalizeSemanticText(queryText)
	if normalized == "" {
		return nil, 0, nil
	}

	entries, err := c.store.ListValidSemanticCacheEntries(ctx, userID, endpoint, model, c.cfg.SemanticMaxCandidates)
	if err != nil || len(entries) == 0 {
		return nil, 0, err
	}

	if c.embedder != nil {
		if matched, score, err := c.lookupSemanticWithEmbeddings(ctx, queryText, entries); err == nil && matched != nil {
			if score < c.cfg.SemanticSimilarity {
				return nil, score, nil
			}
			return &CachedResponse{
				StatusCode:  matched.StatusCode,
				ContentType: matched.ContentType,
				Body:        append([]byte(nil), matched.ResponseBody...),
			}, score, nil
		}
	}

	best, bestScore := lookupSemanticByJaccard(normalized, entries)

	if best == nil || bestScore < c.cfg.SemanticSimilarity {
		return nil, bestScore, nil
	}

	return &CachedResponse{
		StatusCode:  best.StatusCode,
		ContentType: best.ContentType,
		Body:        append([]byte(nil), best.ResponseBody...),
	}, bestScore, nil
}

func (c *ExactCache) Store(ctx context.Context, userID uint, endpoint, model, requestHash, queryText, contentType string, statusCode int, body []byte) error {
	if c == nil || c.store == nil || !c.cfg.Enabled {
		return nil
	}
	if len(body) == 0 || len(body) > c.cfg.MaxBodyBytes {
		return nil
	}

	return c.store.UpsertCacheEntry(ctx, store.CacheEntry{
		UserID:       userID,
		Endpoint:     endpoint,
		RequestHash:  requestHash,
		Model:        model,
		QueryText:    queryText,
		StatusCode:   statusCode,
		ContentType:  contentType,
		ResponseBody: append([]byte(nil), body...),
		ExpiresAt:    time.Now().Add(time.Duration(c.cfg.ExactChatTTL)),
	})
}

func (c *ExactCache) semanticEnabled() bool {
	if c.runtimeSettings == nil {
		return c.cfg.SemanticEnabled
	}
	return c.runtimeSettings.SemanticCacheEnabled()
}

func (c *ExactCache) lookupSemanticWithEmbeddings(ctx context.Context, queryText string, entries []store.CacheEntry) (*store.CacheEntry, float64, error) {
	texts := make([]string, 0, len(entries)+1)
	texts = append(texts, queryText)
	indexMap := make([]int, 0, len(entries))
	for i, entry := range entries {
		candidate := strings.TrimSpace(entry.QueryText)
		if candidate == "" {
			continue
		}
		texts = append(texts, candidate)
		indexMap = append(indexMap, i)
	}
	if len(texts) <= 1 {
		return nil, 0, nil
	}

	vectors, err := c.embedTexts(ctx, texts)
	if err != nil || len(vectors) != len(texts) {
		return nil, 0, err
	}

	queryVector := vectors[0]
	bestScore := 0.0
	var best *store.CacheEntry
	for idx, entryIndex := range indexMap {
		score := cosineSimilarity(queryVector, vectors[idx+1])
		if score > bestScore {
			copied := entries[entryIndex]
			best = &copied
			bestScore = score
		}
	}
	if best == nil {
		return nil, 0, nil
	}
	return best, bestScore, nil
}

func (c *ExactCache) embedTexts(ctx context.Context, texts []string) ([][]float64, error) {
	results := make([][]float64, len(texts))
	missingIndexes := make([]int, 0, len(texts))
	missingTexts := make([]string, 0, len(texts))

	now := time.Now()
	c.embeddingMu.RLock()
	for i, text := range texts {
		key := normalizeEmbeddingText(text)
		if key == "" {
			continue
		}
		memo, ok := c.embeddingMemo[key]
		if ok && memo.expiresAt.After(now) {
			results[i] = append([]float64(nil), memo.vector...)
			continue
		}
		missingIndexes = append(missingIndexes, i)
		missingTexts = append(missingTexts, text)
	}
	c.embeddingMu.RUnlock()

	if len(missingTexts) > 0 {
		embedded, err := c.embedder.Embed(ctx, missingTexts)
		if err != nil {
			return nil, err
		}
		if len(embedded) != len(missingTexts) {
			return nil, nil
		}

		c.embeddingMu.Lock()
		for offset, index := range missingIndexes {
			vector := append([]float64(nil), embedded[offset]...)
			results[index] = vector
			key := normalizeEmbeddingText(texts[index])
			if key != "" && len(vector) > 0 {
				c.embeddingMemo[key] = embeddingMemo{
					vector:    append([]float64(nil), vector...),
					expiresAt: now.Add(30 * time.Minute),
				}
			}
		}
		c.embeddingMu.Unlock()
	}

	return results, nil
}

func lookupSemanticByJaccard(normalizedQuery string, entries []store.CacheEntry) (*store.CacheEntry, float64) {
	bestScore := 0.0
	var best *store.CacheEntry
	for _, entry := range entries {
		score := jaccardSimilarity(normalizedQuery, normalizeSemanticText(entry.QueryText))
		if score > bestScore {
			copied := entry
			best = &copied
			bestScore = score
		}
	}
	return best, bestScore
}

func normalizeEmbeddingText(input string) string {
	input = strings.TrimSpace(strings.ToLower(input))
	if input == "" {
		return ""
	}
	input = strings.ReplaceAll(input, "\r\n", "\n")
	input = strings.ReplaceAll(input, "\r", "\n")
	input = strings.Join(strings.Fields(input), " ")
	return input
}

func normalizeSemanticText(input string) string {
	input = strings.ToLower(strings.TrimSpace(input))
	if input == "" {
		return ""
	}

	fields := strings.FieldsFunc(input, func(r rune) bool {
		switch {
		case r >= 'a' && r <= 'z':
			return false
		case r >= '0' && r <= '9':
			return false
		case r >= 0x4e00 && r <= 0x9fa5:
			return false
		default:
			return true
		}
	})

	sort.Strings(fields)
	return strings.Join(fields, " ")
}

func jaccardSimilarity(left, right string) float64 {
	if left == "" || right == "" {
		return 0
	}

	leftSet := toTokenSet(left)
	rightSet := toTokenSet(right)
	if len(leftSet) == 0 || len(rightSet) == 0 {
		return 0
	}

	intersection := 0
	union := make(map[string]struct{}, len(leftSet)+len(rightSet))
	for token := range leftSet {
		union[token] = struct{}{}
		if _, ok := rightSet[token]; ok {
			intersection++
		}
	}
	for token := range rightSet {
		union[token] = struct{}{}
	}

	return float64(intersection) / float64(len(union))
}

func cosineSimilarity(left, right []float64) float64 {
	if len(left) == 0 || len(right) == 0 || len(left) != len(right) {
		return 0
	}

	dot := 0.0
	leftNorm := 0.0
	rightNorm := 0.0
	for i := range left {
		dot += left[i] * right[i]
		leftNorm += left[i] * left[i]
		rightNorm += right[i] * right[i]
	}
	if leftNorm == 0 || rightNorm == 0 {
		return 0
	}
	return dot / (math.Sqrt(leftNorm) * math.Sqrt(rightNorm))
}

func toTokenSet(input string) map[string]struct{} {
	items := make(map[string]struct{})
	for _, token := range strings.Fields(input) {
		if len([]rune(token)) < 2 {
			continue
		}
		items[token] = struct{}{}
	}
	return items
}
