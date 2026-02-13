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

// ProjectLearning manages project-specific patterns, commands, and preferences.
type ProjectLearning struct {
	path     string
	data     *ProjectData
	mu       sync.RWMutex
	dirty    bool
	timerMu  sync.Mutex
	timer    *time.Timer
}

// ProjectData contains all learned project-specific data.
type ProjectData struct {
	Patterns    []LearnedPattern  `json:"patterns,omitempty"`
	Preferences map[string]string `json:"preferences,omitempty"`
	Commands    []LearnedCommand  `json:"commands,omitempty"`
	FileTypes   []LearnedFileType `json:"file_types,omitempty"`
	LastUpdated time.Time         `json:"last_updated"`
}

// LearnedPattern represents a learned code pattern.
type LearnedPattern struct {
	Name        string    `json:"name"`
	Description string    `json:"description"`
	Examples    []string  `json:"examples,omitempty"`
	UsageCount  int       `json:"usage_count"`
	LastUsed    time.Time `json:"last_used"`
	Tags        []string  `json:"tags,omitempty"`
}

// LearnedCommand represents a learned command with success tracking.
type LearnedCommand struct {
	Command     string    `json:"command"`
	Description string    `json:"description,omitempty"`
	UsageCount  int       `json:"usage_count"`
	LastUsed    time.Time `json:"last_used"`
	SuccessRate float64   `json:"success_rate"`
	AvgDuration float64   `json:"avg_duration_ms,omitempty"`
}

// LearnedFileType tracks conventions for specific file extensions.
type LearnedFileType struct {
	Extension   string   `json:"extension"`
	Conventions []string `json:"conventions,omitempty"`
	UsageCount  int      `json:"usage_count"`
}

// NewProjectLearning creates a new project learning store.
// projectDir is the directory where learning data is persisted.
func NewProjectLearning(projectDir string) (*ProjectLearning, error) {
	if err := os.MkdirAll(projectDir, 0755); err != nil {
		return nil, err
	}

	pl := &ProjectLearning{
		path: filepath.Join(projectDir, "learning.json"),
		data: &ProjectData{
			Preferences: make(map[string]string),
		},
	}

	if err := pl.load(); err != nil && !os.IsNotExist(err) {
		pl.data = &ProjectData{Preferences: make(map[string]string)}
	}

	return pl, nil
}

// RecordPattern records a code pattern.
func (pl *ProjectLearning) RecordPattern(name, description string, examples []string, tags []string) {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	var pattern *LearnedPattern
	for i := range pl.data.Patterns {
		if pl.data.Patterns[i].Name == name {
			pattern = &pl.data.Patterns[i]
			break
		}
	}

	if pattern == nil {
		pl.data.Patterns = append(pl.data.Patterns, LearnedPattern{
			Name: name, Description: description, Examples: examples, Tags: tags,
		})
		pattern = &pl.data.Patterns[len(pl.data.Patterns)-1]
	}

	pattern.UsageCount++
	pattern.LastUsed = time.Now()

	existing := make(map[string]bool)
	for _, ex := range pattern.Examples {
		existing[ex] = true
	}
	for _, ex := range examples {
		if !existing[ex] {
			pattern.Examples = append(pattern.Examples, ex)
		}
	}
	if len(pattern.Examples) > 5 {
		pattern.Examples = pattern.Examples[len(pattern.Examples)-5:]
	}

	pl.dirty = true
	pl.scheduleSave()
}

// GetPatterns returns patterns matching the given category tag.
func (pl *ProjectLearning) GetPatterns(category string) []LearnedPattern {
	pl.mu.RLock()
	defer pl.mu.RUnlock()

	if category == "" {
		result := make([]LearnedPattern, len(pl.data.Patterns))
		copy(result, pl.data.Patterns)
		return result
	}

	catLower := strings.ToLower(category)
	var result []LearnedPattern
	for _, p := range pl.data.Patterns {
		for _, t := range p.Tags {
			if strings.ToLower(t) == catLower {
				result = append(result, p)
				break
			}
		}
	}
	return result
}

// RecordCommand records a command execution with success/failure tracking.
func (pl *ProjectLearning) RecordCommand(cmd string, success bool) {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	var command *LearnedCommand
	for i := range pl.data.Commands {
		if pl.data.Commands[i].Command == cmd {
			command = &pl.data.Commands[i]
			break
		}
	}

	if command == nil {
		pl.data.Commands = append(pl.data.Commands, LearnedCommand{
			Command: cmd, SuccessRate: 1.0,
		})
		command = &pl.data.Commands[len(pl.data.Commands)-1]
	}

	command.UsageCount++
	command.LastUsed = time.Now()

	alpha := 0.3
	if success {
		command.SuccessRate = alpha + (1-alpha)*command.SuccessRate
	} else {
		command.SuccessRate = (1 - alpha) * command.SuccessRate
	}

	pl.dirty = true
	pl.scheduleSave()
}

// SetPreference sets a project preference.
func (pl *ProjectLearning) SetPreference(key, value string) {
	pl.mu.Lock()
	defer pl.mu.Unlock()
	pl.data.Preferences[key] = value
	pl.dirty = true
	pl.scheduleSave()
}

// GetPreference returns a project preference.
func (pl *ProjectLearning) GetPreference(key string) string {
	pl.mu.RLock()
	defer pl.mu.RUnlock()
	return pl.data.Preferences[key]
}

// GetFrequentCommands returns the most frequently used commands.
func (pl *ProjectLearning) GetFrequentCommands(limit int) []LearnedCommand {
	pl.mu.RLock()
	defer pl.mu.RUnlock()

	cmds := make([]LearnedCommand, len(pl.data.Commands))
	copy(cmds, pl.data.Commands)

	sort.Slice(cmds, func(i, j int) bool {
		return cmds[i].UsageCount > cmds[j].UsageCount
	})

	if limit > 0 && len(cmds) > limit {
		return cmds[:limit]
	}
	return cmds
}

// FormatForPrompt returns formatted learning data for prompt injection.
func (pl *ProjectLearning) FormatForPrompt() string {
	pl.mu.RLock()
	defer pl.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("## Project Learning\n\n")

	if len(pl.data.Preferences) > 0 {
		sb.WriteString("### Preferences\n")
		for k, v := range pl.data.Preferences {
			sb.WriteString("- **" + k + "**: " + v + "\n")
		}
		sb.WriteString("\n")
	}

	if len(pl.data.Patterns) > 0 {
		sb.WriteString("### Learned Patterns\n")
		count := 0
		for _, p := range pl.data.Patterns {
			if count >= 5 {
				break
			}
			sb.WriteString("- **" + p.Name + "**: " + p.Description + "\n")
			count++
		}
		sb.WriteString("\n")
	}

	return sb.String()
}

// Flush forces an immediate save.
func (pl *ProjectLearning) Flush() error {
	pl.timerMu.Lock()
	if pl.timer != nil {
		pl.timer.Stop()
		pl.timer = nil
	}
	pl.timerMu.Unlock()

	pl.mu.Lock()
	defer pl.mu.Unlock()
	if !pl.dirty {
		return nil
	}
	err := pl.save()
	if err == nil {
		pl.dirty = false
	}
	return err
}

// --- internal ---

func (pl *ProjectLearning) load() error {
	data, err := os.ReadFile(pl.path)
	if err != nil {
		return err
	}
	var loaded ProjectData
	if err := json.Unmarshal(data, &loaded); err != nil {
		return err
	}
	pl.data = &loaded
	if pl.data.Preferences == nil {
		pl.data.Preferences = make(map[string]string)
	}
	return nil
}

func (pl *ProjectLearning) save() error {
	pl.data.LastUpdated = time.Now()
	data, err := json.MarshalIndent(pl.data, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(pl.path, data, 0644)
}

func (pl *ProjectLearning) scheduleSave() {
	pl.timerMu.Lock()
	defer pl.timerMu.Unlock()
	if pl.timer != nil {
		pl.timer.Stop()
	}
	pl.timer = time.AfterFunc(2*time.Second, func() {
		pl.mu.Lock()
		if !pl.dirty {
			pl.mu.Unlock()
			return
		}
		err := pl.save()
		if err == nil {
			pl.dirty = false
		}
		pl.mu.Unlock()
	})
}
