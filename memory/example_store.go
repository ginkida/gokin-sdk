package memory

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// TaskExample represents a successful task execution for few-shot learning.
type TaskExample struct {
	ID           string            `json:"id"`
	TaskType     string            `json:"task_type"`
	InputPrompt  string            `json:"input_prompt"`
	AgentType    string            `json:"agent_type"`
	ToolsUsed    []string          `json:"tools_used"`
	ToolSequence []ToolCallExample `json:"tool_sequence"`
	FinalOutput  string            `json:"final_output"`
	Duration     time.Duration     `json:"duration"`
	TokensUsed   int               `json:"tokens_used"`
	SuccessScore float64           `json:"success_score"`
	Tags         []string          `json:"tags"`
	Created      time.Time         `json:"created"`
	UseCount     int               `json:"use_count"`
}

// ToolCallExample represents a single tool call in a sequence.
type ToolCallExample struct {
	ToolName string         `json:"tool_name"`
	Args     map[string]any `json:"args"`
	Success  bool           `json:"success"`
	Output   string         `json:"output"`
}

// ExampleStore manages task examples for few-shot learning.
type ExampleStore struct {
	dir      string
	examples map[string]*TaskExample
	byType   map[string][]string
	mu       sync.RWMutex
}

// NewExampleStore creates a new example store.
func NewExampleStore(dir string) (*ExampleStore, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	es := &ExampleStore{
		dir:      dir,
		examples: make(map[string]*TaskExample),
		byType:   make(map[string][]string),
	}

	_ = es.load() // Non-fatal

	return es, nil
}

// RecordExample records a successful task execution.
func (es *ExampleStore) RecordExample(taskType, prompt, agentType, output string, score float64) error {
	return es.RecordExampleWithTools(taskType, prompt, agentType, output, nil, score)
}

// RecordExampleWithTools records a successful task with tool sequence.
func (es *ExampleStore) RecordExampleWithTools(taskType, prompt, agentType, output string, toolSeq []ToolCallExample, score float64) error {
	es.mu.Lock()
	defer es.mu.Unlock()

	tags := extractExampleTags(prompt)

	var toolsUsed []string
	toolSet := make(map[string]bool)
	for _, tc := range toolSeq {
		if !toolSet[tc.ToolName] {
			toolsUsed = append(toolsUsed, tc.ToolName)
			toolSet[tc.ToolName] = true
		}
	}

	truncatedOutput := output
	if len(truncatedOutput) > 2000 {
		truncatedOutput = truncatedOutput[:2000] + "...[truncated]"
	}

	example := &TaskExample{
		ID:           generateID(prompt + taskType),
		TaskType:     taskType,
		InputPrompt:  prompt,
		AgentType:    agentType,
		ToolsUsed:    toolsUsed,
		ToolSequence: toolSeq,
		FinalOutput:  truncatedOutput,
		SuccessScore: score,
		Tags:         tags,
		Created:      time.Now(),
	}

	es.examples[example.ID] = example
	es.byType[taskType] = append(es.byType[taskType], example.ID)

	es.pruneOldExamples(taskType, 50)

	go func() { _ = es.save() }()

	return nil
}

// FindSimilar finds examples similar to the given prompt.
func (es *ExampleStore) FindSimilar(prompt string, limit int) []TaskExampleSummary {
	es.mu.RLock()
	defer es.mu.RUnlock()

	promptTags := extractExampleTags(prompt)
	if len(promptTags) == 0 {
		return nil
	}

	type scored struct {
		example *TaskExample
		score   float64
	}

	var results []scored
	for _, ex := range es.examples {
		overlap := 0
		for _, tag := range promptTags {
			for _, exTag := range ex.Tags {
				if tag == exTag || strings.Contains(exTag, tag) || strings.Contains(tag, exTag) {
					overlap++
					break
				}
			}
		}
		if overlap == 0 {
			continue
		}
		score := (float64(overlap) / float64(len(promptTags))) * ex.SuccessScore
		results = append(results, scored{example: ex, score: score})
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].score > results[j].score
	})

	if limit > len(results) {
		limit = len(results)
	}

	summaries := make([]TaskExampleSummary, limit)
	for i := 0; i < limit; i++ {
		ex := results[i].example
		summaries[i] = TaskExampleSummary{
			ID:        ex.ID,
			TaskType:  ex.TaskType,
			Prompt:    ex.InputPrompt,
			AgentType: ex.AgentType,
			Score:     results[i].score,
		}
	}
	return summaries
}

// TaskExampleSummary is a brief summary of a matched example.
type TaskExampleSummary struct {
	ID        string
	TaskType  string
	Prompt    string
	AgentType string
	Score     float64
}

// Prune removes examples older than maxAge with low scores.
func (es *ExampleStore) Prune(maxAge time.Duration) error {
	es.mu.Lock()
	defer es.mu.Unlock()

	cutoff := time.Now().Add(-maxAge)
	var toDelete []string

	for id, ex := range es.examples {
		if ex.Created.Before(cutoff) && ex.SuccessScore < 0.3 {
			toDelete = append(toDelete, id)
		}
	}

	for _, id := range toDelete {
		ex := es.examples[id]
		delete(es.examples, id)
		ids := es.byType[ex.TaskType]
		for i, eid := range ids {
			if eid == id {
				es.byType[ex.TaskType] = append(ids[:i], ids[i+1:]...)
				break
			}
		}
	}

	return es.save()
}

// Clear removes all examples.
func (es *ExampleStore) Clear() error {
	es.mu.Lock()
	defer es.mu.Unlock()
	es.examples = make(map[string]*TaskExample)
	es.byType = make(map[string][]string)
	return es.save()
}

// --- internal ---

func (es *ExampleStore) storagePath() string {
	return filepath.Join(es.dir, "examples.json")
}

func (es *ExampleStore) load() error {
	data, err := os.ReadFile(es.storagePath())
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var examples map[string]*TaskExample
	if err := json.Unmarshal(data, &examples); err != nil {
		return err
	}
	es.examples = examples
	es.byType = make(map[string][]string)
	for id, ex := range es.examples {
		es.byType[ex.TaskType] = append(es.byType[ex.TaskType], id)
	}
	return nil
}

func (es *ExampleStore) save() error {
	dir := filepath.Dir(es.storagePath())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(es.examples, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(es.storagePath(), data, 0644)
}

func (es *ExampleStore) pruneOldExamples(taskType string, maxCount int) {
	ids := es.byType[taskType]
	if len(ids) <= maxCount {
		return
	}
	sort.Slice(ids, func(i, j int) bool {
		ei := es.examples[ids[i]]
		ej := es.examples[ids[j]]
		if ei.SuccessScore != ej.SuccessScore {
			return ei.SuccessScore > ej.SuccessScore
		}
		return ei.Created.After(ej.Created)
	})
	for _, id := range ids[maxCount:] {
		delete(es.examples, id)
	}
	es.byType[taskType] = ids[:maxCount]
}

func extractExampleTags(prompt string) []string {
	words := strings.Fields(strings.ToLower(prompt))
	tagSet := make(map[string]bool)
	stopWords := map[string]bool{
		"the": true, "a": true, "an": true, "is": true, "are": true,
		"to": true, "for": true, "and": true, "or": true, "in": true,
		"of": true, "that": true, "this": true, "it": true, "with": true,
		"on": true, "be": true, "as": true, "by": true, "at": true,
		"from": true, "can": true, "how": true, "what": true, "where": true,
		"i": true, "you": true, "we": true, "they": true, "my": true,
		"please": true, "help": true, "me": true,
	}
	for _, word := range words {
		word = strings.Trim(word, ".,!?;:'\"()[]{}*")
		if len(word) < 3 || stopWords[word] {
			continue
		}
		tagSet[word] = true
	}
	tags := make([]string, 0, len(tagSet))
	for tag := range tagSet {
		tags = append(tags, tag)
	}
	return tags
}
