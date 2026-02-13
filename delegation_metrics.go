package sdk

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// DelegationMetrics tracks success/failure rates for delegation decisions.
type DelegationMetrics struct {
	// PathMetrics tracks statistics by delegation path: "from_agent:to_agent:context_type"
	PathMetrics map[string]*PathStats `json:"path_metrics"`

	// RuleWeights are adjusted based on historical performance.
	RuleWeights map[string]float64 `json:"rule_weights"`

	// UpdatedAt is the last update timestamp.
	UpdatedAt time.Time `json:"updated_at"`

	configDir string
	mu        sync.RWMutex
}

// PathStats tracks statistics for a specific delegation path.
type PathStats struct {
	FromAgent   string `json:"from_agent"`
	ToAgent     string `json:"to_agent"`
	ContextType string `json:"context_type"`

	SuccessCount int           `json:"success_count"`
	FailureCount int           `json:"failure_count"`
	TotalTime    time.Duration `json:"total_time"`

	// RecentResults stores recent executions for trend analysis.
	RecentResults []DelegationResult `json:"recent_results"`

	LastUsed time.Time `json:"last_used"`
}

// DelegationResult represents a single delegation execution result.
type DelegationResult struct {
	Success   bool          `json:"success"`
	Duration  time.Duration `json:"duration"`
	Timestamp time.Time     `json:"timestamp"`
	ErrorType string        `json:"error_type,omitempty"`
}

const (
	// MaxRecentResults limits the number of recent results to track per path.
	MaxRecentResults = 20

	// MinSamplesForConfidence is the minimum samples needed for confident decisions.
	MinSamplesForConfidence = 5

	// MaxDelegationPaths is the maximum number of delegation paths to track.
	MaxDelegationPaths = 200
)

// NewDelegationMetrics creates a new delegation metrics tracker.
// configDir is the directory where metrics will be persisted (e.g., ~/.config/gokin/).
// Pass "" to disable persistence.
func NewDelegationMetrics(configDir string) *DelegationMetrics {
	dm := &DelegationMetrics{
		PathMetrics: make(map[string]*PathStats),
		RuleWeights: make(map[string]float64),
		configDir:   configDir,
	}

	if configDir != "" {
		dm.load()
	}

	return dm
}

func (dm *DelegationMetrics) storagePath() string {
	return filepath.Join(dm.configDir, "memory", "delegation_metrics.json")
}

func (dm *DelegationMetrics) load() {
	data, err := os.ReadFile(dm.storagePath())
	if err != nil {
		return
	}

	var loaded DelegationMetrics
	if err := json.Unmarshal(data, &loaded); err != nil {
		return
	}

	dm.PathMetrics = loaded.PathMetrics
	dm.RuleWeights = loaded.RuleWeights
	dm.UpdatedAt = loaded.UpdatedAt
}

func (dm *DelegationMetrics) save() ([]byte, error) {
	dm.UpdatedAt = time.Now()
	return json.MarshalIndent(dm, "", "  ")
}

func (dm *DelegationMetrics) writeSnapshot(data []byte) error {
	dir := filepath.Dir(dm.storagePath())
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}
	return os.WriteFile(dm.storagePath(), data, 0644)
}

// RecordExecution records the outcome of a delegation.
func (dm *DelegationMetrics) RecordExecution(fromAgent, toAgent, contextType string, success bool, duration time.Duration, errorType string) {
	dm.mu.Lock()

	key := delegationPathKey(fromAgent, toAgent, contextType)

	stats, ok := dm.PathMetrics[key]
	if !ok {
		stats = &PathStats{
			FromAgent:     fromAgent,
			ToAgent:       toAgent,
			ContextType:   contextType,
			LastUsed:      time.Now(),
			RecentResults: make([]DelegationResult, 0, MaxRecentResults),
		}
		dm.PathMetrics[key] = stats

		if len(dm.PathMetrics) > MaxDelegationPaths {
			dm.evictOldest(MaxDelegationPaths)
		}
	}

	if success {
		stats.SuccessCount++
	} else {
		stats.FailureCount++
	}
	stats.TotalTime += duration
	stats.LastUsed = time.Now()

	stats.RecentResults = append(stats.RecentResults, DelegationResult{
		Success:   success,
		Duration:  duration,
		Timestamp: time.Now(),
		ErrorType: errorType,
	})
	if len(stats.RecentResults) > MaxRecentResults {
		stats.RecentResults = stats.RecentResults[len(stats.RecentResults)-MaxRecentResults:]
	}

	dm.updateRuleWeight(key, success)

	var snapshot []byte
	if dm.configDir != "" {
		snapshot, _ = dm.save()
	}
	dm.mu.Unlock()

	// Async disk write outside of lock
	if snapshot != nil {
		go dm.writeSnapshot(snapshot)
	}
}

func (dm *DelegationMetrics) evictOldest(maxSize int) {
	if len(dm.PathMetrics) <= maxSize {
		return
	}

	type entry struct {
		key      string
		lastUsed time.Time
	}
	entries := make([]entry, 0, len(dm.PathMetrics))
	for k, v := range dm.PathMetrics {
		entries = append(entries, entry{k, v.LastUsed})
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].lastUsed.Before(entries[j].lastUsed)
	})

	toRemove := len(dm.PathMetrics) - maxSize
	for i := 0; i < toRemove; i++ {
		delete(dm.PathMetrics, entries[i].key)
		delete(dm.RuleWeights, entries[i].key)
	}
}

func (dm *DelegationMetrics) updateRuleWeight(key string, success bool) {
	if _, ok := dm.RuleWeights[key]; !ok {
		dm.RuleWeights[key] = 1.0
	}

	alpha := 0.1
	if success {
		dm.RuleWeights[key] = dm.RuleWeights[key]*(1-alpha) + 1.2*alpha
	} else {
		dm.RuleWeights[key] = dm.RuleWeights[key]*(1-alpha) + 0.8*alpha
	}

	// Clamp to [0.5, 2.0]
	if dm.RuleWeights[key] < 0.5 {
		dm.RuleWeights[key] = 0.5
	}
	if dm.RuleWeights[key] > 2.0 {
		dm.RuleWeights[key] = 2.0
	}
}

// GetSuccessRate returns the success rate for a delegation path.
func (dm *DelegationMetrics) GetSuccessRate(fromAgent, toAgent, contextType string) float64 {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	key := delegationPathKey(fromAgent, toAgent, contextType)
	stats, ok := dm.PathMetrics[key]
	if !ok {
		return 0.5 // Default neutral
	}

	total := stats.SuccessCount + stats.FailureCount
	if total == 0 {
		return 0.5
	}
	return float64(stats.SuccessCount) / float64(total)
}

// GetRuleWeight returns the weight for a delegation rule.
func (dm *DelegationMetrics) GetRuleWeight(fromAgent, toAgent, contextType string) float64 {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	key := delegationPathKey(fromAgent, toAgent, contextType)
	weight, ok := dm.RuleWeights[key]
	if !ok {
		return 1.0
	}
	return weight
}

// GetRecentTrend analyzes recent executions to determine performance trend.
// Returns -1.0 (declining) to 1.0 (improving).
func (dm *DelegationMetrics) GetRecentTrend(fromAgent, toAgent, contextType string) float64 {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	key := delegationPathKey(fromAgent, toAgent, contextType)
	stats, ok := dm.PathMetrics[key]
	if !ok || len(stats.RecentResults) < MinSamplesForConfidence {
		return 0
	}

	mid := len(stats.RecentResults) / 2
	firstRate := delegationSuccessRate(stats.RecentResults[:mid])
	secondRate := delegationSuccessRate(stats.RecentResults[mid:])

	return secondRate - firstRate
}

// GetBestTarget returns the best delegation target based on historical data.
func (dm *DelegationMetrics) GetBestTarget(fromAgent, contextType string, candidates []string) string {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	if len(candidates) == 0 {
		return ""
	}

	bestCandidate := candidates[0]
	bestScore := 0.0

	for _, candidate := range candidates {
		key := delegationPathKey(fromAgent, candidate, contextType)

		stats, ok := dm.PathMetrics[key]
		if !ok {
			continue
		}

		total := stats.SuccessCount + stats.FailureCount
		if total < MinSamplesForConfidence {
			continue
		}

		successRate := float64(stats.SuccessCount) / float64(total)
		weight := dm.RuleWeights[key]
		if weight == 0 {
			weight = 1.0
		}

		// Compute trend without lock (already held)
		trend := 0.0
		if len(stats.RecentResults) >= MinSamplesForConfidence {
			mid := len(stats.RecentResults) / 2
			trend = delegationSuccessRate(stats.RecentResults[mid:]) - delegationSuccessRate(stats.RecentResults[:mid])
		}

		score := successRate*weight + trend*0.1
		if score > bestScore {
			bestScore = score
			bestCandidate = candidate
		}
	}

	return bestCandidate
}

// ShouldUseDelegation returns whether delegation should be used based on historical performance.
func (dm *DelegationMetrics) ShouldUseDelegation(fromAgent, toAgent, contextType string) bool {
	successRate := dm.GetSuccessRate(fromAgent, toAgent, contextType)
	weight := dm.GetRuleWeight(fromAgent, toAgent, contextType)
	trend := dm.GetRecentTrend(fromAgent, toAgent, contextType)

	threshold := 0.3
	if trend < -0.2 {
		threshold = 0.4
	} else if trend > 0.2 {
		threshold = 0.2
	}

	return successRate*weight >= threshold
}

// GetStats returns overall statistics.
func (dm *DelegationMetrics) GetStats() map[string]any {
	dm.mu.RLock()
	defer dm.mu.RUnlock()

	totalPaths := len(dm.PathMetrics)
	totalExecutions := 0
	totalSuccesses := 0

	for _, stats := range dm.PathMetrics {
		totalExecutions += stats.SuccessCount + stats.FailureCount
		totalSuccesses += stats.SuccessCount
	}

	overallRate := 0.0
	if totalExecutions > 0 {
		overallRate = float64(totalSuccesses) / float64(totalExecutions)
	}

	return map[string]any{
		"total_paths":          totalPaths,
		"total_executions":     totalExecutions,
		"overall_success_rate": overallRate,
		"last_updated":         dm.UpdatedAt,
	}
}

// Clear removes all metrics.
func (dm *DelegationMetrics) Clear() error {
	dm.mu.Lock()
	dm.PathMetrics = make(map[string]*PathStats)
	dm.RuleWeights = make(map[string]float64)

	if dm.configDir == "" {
		dm.mu.Unlock()
		return nil
	}

	snapshot, err := dm.save()
	dm.mu.Unlock()

	if err != nil {
		return err
	}
	return dm.writeSnapshot(snapshot)
}

func delegationPathKey(fromAgent, toAgent, contextType string) string {
	return fromAgent + ":" + toAgent + ":" + contextType
}

func delegationSuccessRate(results []DelegationResult) float64 {
	if len(results) == 0 {
		return 0.5
	}
	successes := 0
	for _, r := range results {
		if r.Success {
			successes++
		}
	}
	return float64(successes) / float64(len(results))
}
