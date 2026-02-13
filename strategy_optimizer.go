package sdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// StrategyMetrics tracks performance metrics for a strategy.
type StrategyMetrics struct {
	StrategyName string         `json:"strategy_name"`
	SuccessCount int            `json:"success_count"`
	FailureCount int            `json:"failure_count"`
	TotalTime    time.Duration  `json:"total_time"`
	AvgDuration  time.Duration  `json:"avg_duration"`
	LastUsed     time.Time      `json:"last_used"`
	TaskTypes    map[string]int `json:"task_types"`
}

// SuccessRate returns the success rate as a float64.
func (sm *StrategyMetrics) SuccessRate() float64 {
	total := sm.SuccessCount + sm.FailureCount
	if total == 0 {
		return 0.5
	}
	return float64(sm.SuccessCount) / float64(total)
}

// StrategyOptimizer tracks and optimizes strategy choices with composite scoring.
type StrategyOptimizer struct {
	metrics   map[string]*StrategyMetrics
	storePath string
	mu        sync.RWMutex
}

// NewStrategyOptimizer creates a new strategy optimizer.
// storePath is the JSON file used for persistence.
func NewStrategyOptimizer(storePath string) *StrategyOptimizer {
	so := &StrategyOptimizer{
		metrics:   make(map[string]*StrategyMetrics),
		storePath: storePath,
	}
	_ = so.load()
	return so
}

// RecordOutcome records the outcome of a strategy execution.
func (so *StrategyOptimizer) RecordOutcome(taskType, strategy string, success bool, duration time.Duration) {
	so.mu.Lock()

	m, ok := so.metrics[strategy]
	if !ok {
		m = &StrategyMetrics{
			StrategyName: strategy,
			TaskTypes:    make(map[string]int),
		}
		so.metrics[strategy] = m
	}

	if success {
		m.SuccessCount++
	} else {
		m.FailureCount++
	}

	m.TotalTime += duration
	total := m.SuccessCount + m.FailureCount
	m.AvgDuration = m.TotalTime / time.Duration(total)
	m.LastUsed = time.Now()
	m.TaskTypes[taskType]++

	// Marshal while still holding the lock to avoid racing with save()
	data, merr := json.MarshalIndent(so.metrics, "", "  ")
	so.mu.Unlock()

	if merr == nil {
		go func() {
			dir := filepath.Dir(so.storePath)
			_ = os.MkdirAll(dir, 0755)
			_ = os.WriteFile(so.storePath, data, 0644)
		}()
	}
}

// GetBestStrategy returns the best strategy for a task type using composite scoring.
// Score = success_rate + experience_boost - recency_penalty.
func (so *StrategyOptimizer) GetBestStrategy(taskType string) string {
	so.mu.RLock()
	defer so.mu.RUnlock()

	type scored struct {
		name  string
		score float64
	}

	var scores []scored
	for name, m := range so.metrics {
		baseScore := m.SuccessRate()

		// Experience boost for this task type
		taskTypeCount := m.TaskTypes[taskType]
		if taskTypeCount > 0 {
			total := m.SuccessCount + m.FailureCount
			if total > 0 {
				experienceBoost := float64(taskTypeCount) / float64(total)
				baseScore += experienceBoost * 0.2
			}
		}

		// Recency penalty
		daysSinceUse := time.Since(m.LastUsed).Hours() / 24
		if daysSinceUse > 30 {
			baseScore *= 0.9
		}

		scores = append(scores, scored{name: name, score: baseScore})
	}

	if len(scores) == 0 {
		return "general"
	}

	sort.Slice(scores, func(i, j int) bool {
		return scores[i].score > scores[j].score
	})

	return scores[0].name
}

// GetStrategies returns all strategy metrics.
func (so *StrategyOptimizer) GetStrategies() map[string]*StrategyMetrics {
	so.mu.RLock()
	defer so.mu.RUnlock()

	result := make(map[string]*StrategyMetrics)
	for k, v := range so.metrics {
		result[k] = v
	}
	return result
}

// Clear removes all metrics.
func (so *StrategyOptimizer) Clear() error {
	so.mu.Lock()
	defer so.mu.Unlock()
	so.metrics = make(map[string]*StrategyMetrics)
	return so.save()
}

// --- internal ---

func (so *StrategyOptimizer) load() error {
	data, err := os.ReadFile(so.storePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	var metrics map[string]*StrategyMetrics
	if err := json.Unmarshal(data, &metrics); err != nil {
		return err
	}
	so.metrics = metrics
	return nil
}

func (so *StrategyOptimizer) save() error {
	dir := filepath.Dir(so.storePath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(so.metrics, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(so.storePath, data, 0644)
}
