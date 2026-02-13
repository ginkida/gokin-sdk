package context

import (
	"sort"
	"strings"

	"google.golang.org/genai"
)

// MessagePriority represents the importance level of a message.
type MessagePriority int

const (
	PriorityLow      MessagePriority = 0 // Verbose logs, trivial reads
	PriorityNormal   MessagePriority = 1 // Normal messages
	PriorityHigh     MessagePriority = 2 // File edits, important decisions
	PriorityCritical MessagePriority = 3 // System prompts, errors
)

// ScoredMessage pairs a message with its importance score.
type ScoredMessage struct {
	Message  *genai.Content
	Score    float64
	Priority MessagePriority
	Index    int
}

// MessageScore represents the importance score and metadata for a message.
type MessageScore struct {
	Priority    MessagePriority
	Score       float64 // 0.0 - 1.0
	Reason      string  // Explanation of the score
	IsSystem    bool
	HasFileEdit bool
	HasError    bool
	ToolsUsed   []string
	References  []string // File paths, function names
}

// MessageScorer scores messages by importance for context retention.
type MessageScorer struct {
	criticalTools    []string
	verboseTools     []string
	criticalToolsMap map[string]bool
	verboseToolsMap  map[string]bool
}

// NewMessageScorer creates a new message scorer.
func NewMessageScorer() *MessageScorer {
	ms := &MessageScorer{
		criticalTools: []string{"edit", "write", "bash", "git_commit"},
		verboseTools:  []string{"grep", "glob", "tree", "list_dir", "read", "git_log", "env", "task_output"},
		criticalToolsMap: map[string]bool{
			"write": true,
			"edit":  true,
			"bash":  true,
		},
		verboseToolsMap: map[string]bool{
			"read":        true,
			"list_dir":    true,
			"tree":        true,
			"glob":        true,
			"git_log":     true,
			"env":         true,
			"task_output": true,
		},
	}
	return ms
}

// Score returns an importance score for a message.
func (ms *MessageScorer) Score(msg *genai.Content) float64 {
	score := 0.0

	// User messages are always important
	if msg.Role == "user" {
		score += 3.0
	}

	for _, part := range msg.Parts {
		// Text content scoring
		if part.Text != "" {
			text := strings.ToLower(part.Text)

			// Error indicators
			if strings.Contains(text, "error") || strings.Contains(text, "failed") || strings.Contains(text, "panic") {
				score += 2.0
			}

			// Decision/conclusion indicators
			if strings.Contains(text, "decision") || strings.Contains(text, "conclusion") || strings.Contains(text, "solution") {
				score += 1.5
			}

			// System-level content
			if strings.Contains(text, "## system") || strings.HasPrefix(text, "system:") {
				score += 2.0
			}

			// File references
			if strings.Contains(text, ".go") || strings.Contains(text, ".py") || strings.Contains(text, ".ts") {
				score += 0.5
			}
		}

		// Function calls — critical tools score higher
		if part.FunctionCall != nil {
			score += 0.5
			for _, tool := range ms.criticalTools {
				if part.FunctionCall.Name == tool {
					score += 1.0
					break
				}
			}
		}

		// Function responses
		if part.FunctionResponse != nil {
			score += 0.3
			// Check for errors in responses
			if resp := part.FunctionResponse.Response; resp != nil {
				if errVal, ok := resp["error"]; ok {
					if errStr, ok := errVal.(string); ok && errStr != "" {
						score += 2.0
					}
				}
			}
		}
	}

	return score
}

// ScoreMessage evaluates a single message and returns its detailed importance score.
func (ms *MessageScorer) ScoreMessage(msg *genai.Content) MessageScore {
	score := MessageScore{
		Priority:   PriorityNormal,
		Score:      0.5,
		Reason:     "normal message",
		ToolsUsed:  make([]string, 0),
		References: make([]string, 0),
	}

	// Check role
	if msg.Role == "user" {
		score.Score += 0.1
	}

	// Analyze parts
	for _, part := range msg.Parts {
		ms.scoreTextPart(&score, part.Text)
		ms.scoreFunctionCall(&score, part.FunctionCall)
		ms.scoreFunctionResponse(&score, part.FunctionResponse)
	}

	// Determine final priority based on score
	if score.IsSystem || score.HasError {
		score.Priority = PriorityCritical
		score.Score = 1.0
	} else if score.HasFileEdit {
		score.Priority = PriorityHigh
		score.Score = 0.8
	} else if score.Score < 0.3 {
		score.Priority = PriorityLow
	} else if score.Score > 0.7 {
		score.Priority = PriorityHigh
	}

	return score
}

// scoreTextPart analyzes text content for importance indicators.
func (ms *MessageScorer) scoreTextPart(score *MessageScore, text string) {
	if text == "" {
		return
	}

	lower := strings.ToLower(text)

	// Check for system indicators
	if strings.Contains(lower, "system prompt") ||
		strings.Contains(lower, "instructions") ||
		strings.Contains(lower, "context preservation") {
		score.IsSystem = true
		score.Reason = "system instructions"
		score.Score += 0.4
	}

	// Check for decision keywords
	decisionKeywords := []string{
		"decided to", "will implement", "going to", "plan to",
		"summary", "conclusion", "resolved", "fixed",
	}
	for _, keyword := range decisionKeywords {
		if strings.Contains(lower, keyword) {
			score.Score += 0.1
		}
	}

	// Check for error indicators
	errorKeywords := []string{
		"error", "failed", "exception", "bug", "issue",
		"problem", "warning", "not found",
	}
	for _, keyword := range errorKeywords {
		if strings.Contains(lower, keyword) {
			score.HasError = true
			score.Score += 0.2
		}
	}

	// Check for file references (simple heuristic)
	if strings.Contains(text, ".go") ||
		strings.Contains(text, ".md") ||
		strings.Contains(text, ".yaml") ||
		strings.Contains(text, ".json") {
		words := strings.Fields(text)
		for _, word := range words {
			if strings.Contains(word, "/") &&
				(strings.HasSuffix(word, ".go") ||
					strings.HasSuffix(word, ".md") ||
					strings.HasSuffix(word, ".yaml") ||
					strings.HasSuffix(word, ".json")) {
				score.References = append(score.References, strings.Trim(word, "`'\""))
			}
		}
	}
}

// scoreFunctionCall analyzes function calls for importance.
func (ms *MessageScorer) scoreFunctionCall(score *MessageScore, fc *genai.FunctionCall) {
	if fc == nil {
		return
	}

	score.ToolsUsed = append(score.ToolsUsed, fc.Name)

	// Check if it's a critical tool (file modifications)
	if ms.criticalToolsMap[fc.Name] {
		score.HasFileEdit = true
		score.Score += 0.3
		score.Reason = "file modification operation"

		// Extract file paths from args
		if path, ok := fc.Args["file_path"].(string); ok {
			score.References = append(score.References, path)
		}
		if path, ok := fc.Args["path"].(string); ok {
			score.References = append(score.References, path)
		}
	}

	// Check if it's a verbose tool (information gathering)
	if ms.verboseToolsMap[fc.Name] {
		score.Score -= 0.1
		if score.Score < 0.2 {
			score.Score = 0.2
		}
		if score.Reason == "normal message" {
			score.Reason = "information gathering"
		}
	}
}

// scoreFunctionResponse analyzes function responses for importance.
func (ms *MessageScorer) scoreFunctionResponse(score *MessageScore, fr *genai.FunctionResponse) {
	if fr == nil {
		return
	}

	// Check for errors in response
	if fr.Response != nil {
		if errMsg, ok := fr.Response["error"].(string); ok && errMsg != "" {
			score.HasError = true
			score.Score += 0.3
		}

		// Check content size - very large responses are less important
		if content, ok := fr.Response["content"].(string); ok {
			if len(content) > 5000 {
				score.Score -= 0.1
			}
		}

		// Check for success indicators
		if success, ok := fr.Response["success"].(bool); ok && success {
			score.Score += 0.05
		}
	}
}

// ScoreMessages scores a batch of messages.
func (ms *MessageScorer) ScoreMessages(messages []*genai.Content) []MessageScore {
	scores := make([]MessageScore, len(messages))
	for i, msg := range messages {
		scores[i] = ms.ScoreMessage(msg)
	}
	return scores
}

// SelectImportantMessages selects messages to keep based on scores and target count.
// It uses a combination of priority and recency to make decisions.
func (ms *MessageScorer) SelectImportantMessages(
	messages []*genai.Content,
	scores []MessageScore,
	keepCount int,
) []*genai.Content {
	if len(messages) <= keepCount {
		return messages
	}

	// Always keep critical priority messages
	selected := make([]*genai.Content, 0, keepCount)
	selectedIndices := make(map[int]bool)

	// First pass: add all critical messages
	for i, score := range scores {
		if score.Priority == PriorityCritical && len(selected) < keepCount {
			selected = append(selected, messages[i])
			selectedIndices[i] = true
		}
	}

	// Second pass: add high priority messages
	for i, score := range scores {
		if score.Priority == PriorityHigh && !selectedIndices[i] && len(selected) < keepCount {
			selected = append(selected, messages[i])
			selectedIndices[i] = true
		}
	}

	// Third pass: fill with most recent messages if space remains
	if len(selected) < keepCount {
		for i := len(messages) - 1; i >= 0 && len(selected) < keepCount; i-- {
			if !selectedIndices[i] {
				selected = append(selected, messages[i])
				selectedIndices[i] = true
			}
		}
	}

	return selected
}

// CalculateTokenBudget calculates how many tokens should be allocated for messages
// based on their importance scores.
func (ms *MessageScorer) CalculateTokenBudget(
	scores []MessageScore,
	totalBudget int,
) []int {
	if len(scores) == 0 {
		return []int{}
	}

	// Calculate total score
	totalScore := 0.0
	for _, score := range scores {
		totalScore += float64(score.Priority) + score.Score
	}

	// Allocate budget proportionally
	budgets := make([]int, len(scores))
	for i, score := range scores {
		weight := (float64(score.Priority) + score.Score) / totalScore
		budgets[i] = int(float64(totalBudget) * weight)
		if budgets[i] < 100 { // Minimum allocation
			budgets[i] = 100
		}
	}

	return budgets
}

// RankMessages scores and ranks all messages by importance.
func (ms *MessageScorer) RankMessages(messages []*genai.Content) []ScoredMessage {
	scored := make([]ScoredMessage, len(messages))

	for i, msg := range messages {
		s := ms.Score(msg)

		// Recency boost: more recent messages get a boost
		recencyBoost := float64(i) / float64(len(messages)) * 1.0
		s += recencyBoost

		priority := PriorityNormal
		if s >= 4.0 {
			priority = PriorityCritical
		} else if s >= 2.5 {
			priority = PriorityHigh
		} else if s < 1.0 {
			priority = PriorityLow
		}

		scored[i] = ScoredMessage{
			Message:  msg,
			Score:    s,
			Priority: priority,
			Index:    i,
		}
	}

	sort.Slice(scored, func(i, j int) bool {
		return scored[i].Score > scored[j].Score
	})

	return scored
}

// SelectImportant selects the most important messages within a token budget.
func (ms *MessageScorer) SelectImportant(messages []*genai.Content, counter *TokenCounter, maxTokens int) []*genai.Content {
	ranked := ms.RankMessages(messages)

	var selected []ScoredMessage
	tokenCount := 0

	for _, sm := range ranked {
		est := counter.EstimateMessages([]*genai.Content{sm.Message})
		if tokenCount+est > maxTokens {
			continue
		}
		selected = append(selected, sm)
		tokenCount += est
	}

	// Restore original order
	sort.Slice(selected, func(i, j int) bool {
		return selected[i].Index < selected[j].Index
	})

	result := make([]*genai.Content, len(selected))
	for i, sm := range selected {
		result[i] = sm.Message
	}

	return result
}
