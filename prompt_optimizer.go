package sdk

import (
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// PromptVariant represents a variation of a prompt with performance metrics.
type PromptVariant struct {
	ID           string        `json:"id"`
	BasePrompt   string        `json:"base_prompt"`
	Variation    string        `json:"variation"`
	SuccessRate  float64       `json:"success_rate"`
	AvgTokens    int           `json:"avg_tokens"`
	AvgDuration  time.Duration `json:"avg_duration"`
	UseCount     int           `json:"use_count"`
	SuccessCount int           `json:"success_count"`
	FailureCount int           `json:"failure_count"`
	LastUsed     time.Time     `json:"last_used"`
	Created      time.Time     `json:"created"`
}

// Score calculates a combined score for ranking variants.
func (pv *PromptVariant) Score() float64 {
	baseScore := pv.SuccessRate
	confidence := float64(pv.UseCount) / 100.0
	if confidence > 0.2 {
		confidence = 0.2
	}
	return baseScore + confidence
}

// PromptOptimizer A/B tests prompt variants and tracks performance.
type PromptOptimizer struct {
	storePath string
	variants  map[string]*PromptVariant
	byBase    map[string][]string
	mu        sync.RWMutex
}

// NewPromptOptimizer creates a new prompt optimizer.
func NewPromptOptimizer(storePath string) *PromptOptimizer {
	po := &PromptOptimizer{
		storePath: storePath,
		variants:  make(map[string]*PromptVariant),
		byBase:    make(map[string][]string),
	}
	_ = po.load()
	return po
}

// RecordOutcome records the outcome of a prompt execution.
func (po *PromptOptimizer) RecordOutcome(promptKey, variant string, success bool, tokens int, duration time.Duration) {
	po.mu.Lock()

	var v *PromptVariant
	for _, existing := range po.variants {
		if existing.BasePrompt == promptKey && existing.Variation == variant {
			v = existing
			break
		}
	}

	if v == nil {
		v = &PromptVariant{
			ID:         time.Now().Format("20060102150405.000"),
			BasePrompt: promptKey,
			Variation:  variant,
			Created:    time.Now(),
		}
		po.variants[v.ID] = v
		po.byBase[promptKey] = append(po.byBase[promptKey], v.ID)
	}

	v.UseCount++
	v.LastUsed = time.Now()

	if success {
		v.SuccessCount++
	} else {
		v.FailureCount++
	}

	total := v.SuccessCount + v.FailureCount
	v.SuccessRate = float64(v.SuccessCount) / float64(total)

	if tokens > 0 {
		v.AvgTokens = (v.AvgTokens*(v.UseCount-1) + tokens) / v.UseCount
	}
	if duration > 0 {
		v.AvgDuration = (v.AvgDuration*time.Duration(v.UseCount-1) + duration) / time.Duration(v.UseCount)
	}

	// Marshal while still holding the lock to avoid racing with save()
	data, merr := json.MarshalIndent(po.variants, "", "  ")
	po.mu.Unlock()

	if merr == nil {
		go func() {
			dir := filepath.Dir(po.storePath)
			if err := os.MkdirAll(dir, 0755); err != nil {
				slog.Warn("failed to create prompt store directory", "error", err)
				return
			}
			if err := os.WriteFile(po.storePath, data, 0644); err != nil {
				slog.Warn("failed to save prompt metrics", "error", err)
			}
		}()
	}
}

// GetBestVariant returns the best performing variant for a prompt key.
func (po *PromptOptimizer) GetBestVariant(promptKey string) (*PromptVariant, bool) {
	po.mu.RLock()
	defer po.mu.RUnlock()

	ids, ok := po.byBase[promptKey]
	if !ok || len(ids) == 0 {
		return nil, false
	}

	var best *PromptVariant
	bestScore := -1.0

	for _, id := range ids {
		v := po.variants[id]
		if v == nil {
			continue
		}
		if score := v.Score(); score > bestScore {
			bestScore = score
			best = v
		}
	}

	return best, best != nil
}

// GetVariants returns all variants for a prompt key, sorted by score.
func (po *PromptOptimizer) GetVariants(promptKey string) []*PromptVariant {
	po.mu.RLock()
	defer po.mu.RUnlock()

	ids := po.byBase[promptKey]
	variants := make([]*PromptVariant, 0, len(ids))
	for _, id := range ids {
		if v := po.variants[id]; v != nil {
			variants = append(variants, v)
		}
	}

	sort.Slice(variants, func(i, j int) bool {
		return variants[i].Score() > variants[j].Score()
	})

	return variants
}

// Clear removes all variants.
func (po *PromptOptimizer) Clear() error {
	po.mu.Lock()
	defer po.mu.Unlock()
	po.variants = make(map[string]*PromptVariant)
	po.byBase = make(map[string][]string)
	return po.save()
}

// --- internal ---

func (po *PromptOptimizer) load() error {
	data, err := os.ReadFile(po.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var variants map[string]*PromptVariant
	if err := json.Unmarshal(data, &variants); err != nil {
		return err
	}
	po.variants = variants
	po.byBase = make(map[string][]string)
	for id, v := range po.variants {
		po.byBase[v.BasePrompt] = append(po.byBase[v.BasePrompt], id)
	}
	return nil
}

func (po *PromptOptimizer) save() error {
	dir := filepath.Dir(po.storePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(po.variants, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(po.storePath, data, 0644)
}
