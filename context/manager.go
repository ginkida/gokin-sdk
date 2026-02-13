package context

import (
	"context"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// ContextOption configures the ContextManager.
type ContextOption func(*ContextManager)

// WithMaxTokens sets the maximum token budget.
func WithMaxTokens(n int) ContextOption {
	return func(cm *ContextManager) {
		cm.maxTokens = n
	}
}

// WithSummarizationThreshold sets the threshold (0.0-1.0) at which summarization triggers.
func WithSummarizationThreshold(t float64) ContextOption {
	return func(cm *ContextManager) {
		cm.threshold = t
	}
}

// WithCompactorMaxChars sets the max chars for tool output compaction.
func WithCompactorMaxChars(n int) ContextOption {
	return func(cm *ContextManager) {
		cm.compactor = NewCompactor(n)
	}
}

// WithPredictor enables predictive file loading.
func WithPredictor(p *Predictor) ContextOption {
	return func(cm *ContextManager) {
		cm.predictor = p
	}
}

// WithScorer sets a custom message scorer for context retention.
func WithScorer(s *MessageScorer) ContextOption {
	return func(cm *ContextManager) {
		cm.scorer = s
	}
}

// WithSummaryCache enables caching of generated summaries.
func WithSummaryCache(sc *SummaryCache) ContextOption {
	return func(cm *ContextManager) {
		cm.summaryCache = sc
	}
}

// WithStrategy sets the summarization strategy.
func WithStrategy(s *SummaryStrategy) ContextOption {
	return func(cm *ContextManager) {
		cm.strategy = s
	}
}

// ContextManager orchestrates token counting, summarization, and context optimization.
type ContextManager struct {
	client    sdk.Client
	counter   *TokenCounter
	compactor *Compactor
	summarizer *Summarizer

	predictor    *Predictor
	scorer       *MessageScorer
	summaryCache *SummaryCache
	strategy     *SummaryStrategy

	maxTokens int
	threshold float64 // 0.0-1.0, triggers summarization
}

// NewContextManager creates a new context manager.
func NewContextManager(client sdk.Client, opts ...ContextOption) *ContextManager {
	cm := &ContextManager{
		client:    client,
		counter:   NewTokenCounter(),
		compactor: NewCompactor(10000),
		summarizer: NewSummarizer(client),
		maxTokens: 100000,
		threshold: 0.75,
	}

	for _, opt := range opts {
		opt(cm)
	}

	return cm
}

// ShouldSummarize returns true if the history is approaching the token limit.
func (cm *ContextManager) ShouldSummarize(history []*genai.Content) bool {
	tokens := cm.counter.EstimateMessages(history)
	return float64(tokens) > float64(cm.maxTokens)*cm.threshold
}

// Optimize reduces the history to fit within token limits.
// It summarizes older messages while keeping recent ones intact.
// When a scorer is configured, it uses importance-based selection for recent messages.
// When a summary cache is configured, it caches generated summaries.
func (cm *ContextManager) Optimize(ctx context.Context, history []*genai.Content) ([]*genai.Content, error) {
	tokens := cm.counter.EstimateMessages(history)

	// If within budget, no optimization needed
	if float64(tokens) <= float64(cm.maxTokens)*cm.threshold {
		return history, nil
	}

	// Determine keepRecent from strategy or default
	keepRecent := 10
	if cm.strategy != nil {
		keepRecent = cm.strategy.KeepRecentCount
	}
	if keepRecent > len(history) {
		keepRecent = len(history)
	}

	if len(history) <= keepRecent {
		return history, nil
	}

	olderMessages := history[:len(history)-keepRecent]
	recentMessages := history[len(history)-keepRecent:]

	// If scorer is available, use importance-based selection for recent messages
	if cm.scorer != nil {
		targetTokens := int(float64(cm.maxTokens) * 0.6) // 60% budget for recent
		recentMessages = cm.scorer.SelectImportant(recentMessages, cm.counter, targetTokens)
	}

	// Check summary cache
	if cm.summaryCache != nil {
		cacheKey := HashMessages(olderMessages)
		if cached, ok := cm.summaryCache.Get(cacheKey); ok {
			summaryContent := &genai.Content{
				Role:  "user",
				Parts: []*genai.Part{{Text: "[Previous context summary]\n" + cached}},
			}
			result := make([]*genai.Content, 0, len(recentMessages)+1)
			result = append(result, summaryContent)
			result = append(result, recentMessages...)
			return result, nil
		}
	}

	// Summarize older messages
	summaryContent, err := cm.summarizer.SummarizeToContent(ctx, olderMessages)
	if err != nil {
		// If summarization fails, do simple truncation
		return recentMessages, nil
	}

	// Cache the summary if cache is available
	if cm.summaryCache != nil && summaryContent != nil {
		cacheKey := HashMessages(olderMessages)
		if len(summaryContent.Parts) > 0 && summaryContent.Parts[0].Text != "" {
			cm.summaryCache.Set(cacheKey, summaryContent.Parts[0].Text)
		}
	}

	// Return summary + recent messages
	result := make([]*genai.Content, 0, len(recentMessages)+1)
	result = append(result, summaryContent)
	result = append(result, recentMessages...)

	return result, nil
}

// CompactToolResult applies intelligent compaction to tool output.
func (cm *ContextManager) CompactToolResult(toolName, content string) string {
	return cm.compactor.CompactForTool(toolName, content)
}

// EstimateTokens returns an estimated token count for the given content.
func (cm *ContextManager) EstimateTokens(content string) int {
	return cm.counter.Estimate(content)
}

// EstimateHistoryTokens returns an estimated token count for message history.
func (cm *ContextManager) EstimateHistoryTokens(history []*genai.Content) int {
	return cm.counter.EstimateMessages(history)
}

// TokenCounter returns the underlying token counter.
func (cm *ContextManager) TokenCounter() *TokenCounter {
	return cm.counter
}

// Compactor returns the underlying compactor.
func (cm *ContextManager) Compactor() *Compactor {
	return cm.compactor
}

// Predictor returns the underlying file predictor, or nil if not configured.
func (cm *ContextManager) Predictor() *Predictor {
	return cm.predictor
}

// Scorer returns the underlying message scorer, or nil if not configured.
func (cm *ContextManager) Scorer() *MessageScorer {
	return cm.scorer
}

// Strategy returns the current summarization strategy, or nil if using defaults.
func (cm *ContextManager) Strategy() *SummaryStrategy {
	return cm.strategy
}

// Close cleans up resources (e.g., summary cache cleanup goroutine).
func (cm *ContextManager) Close() {
	if cm.summaryCache != nil {
		cm.summaryCache.Close()
	}
}
