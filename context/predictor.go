package context

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// FileAccessPattern tracks how files are accessed during agent execution.
type FileAccessPattern struct {
	Path       string    `json:"path"`
	AccessedAt time.Time `json:"accessed_at"`
	AccessType string    `json:"access_type"` // read, write, grep, edit
	FromFile   string    `json:"from_file"`
}

// CoAccessEntry tracks files frequently accessed together.
type CoAccessEntry struct {
	FileA    string    `json:"file_a"`
	FileB    string    `json:"file_b"`
	Count    int       `json:"count"`
	Strength float64   `json:"strength"`
	LastSeen time.Time `json:"last_seen"`
}

// PredictedFile represents a file that might be needed.
type PredictedFile struct {
	Path       string  `json:"path"`
	Confidence float64 `json:"confidence"`
	Reason     string  `json:"reason"`
}

// Predictor predicts which files might be needed based on access patterns.
type Predictor struct {
	accessHistory []FileAccessPattern
	coAccess      map[string]*CoAccessEntry
	typeRelations map[string]int
	importGraph   map[string][]string
	workDir       string
	mu            sync.RWMutex
}

// NewPredictor creates a new file access predictor.
func NewPredictor(workDir string) *Predictor {
	return &Predictor{
		accessHistory: make([]FileAccessPattern, 0),
		coAccess:      make(map[string]*CoAccessEntry),
		typeRelations: make(map[string]int),
		importGraph:   make(map[string][]string),
		workDir:       workDir,
	}
}

// RecordAccess records a file access for pattern learning.
func (p *Predictor) RecordAccess(path, accessType, fromFile string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	pattern := FileAccessPattern{
		Path:       path,
		AccessedAt: time.Now(),
		AccessType: accessType,
		FromFile:   fromFile,
	}

	p.accessHistory = append(p.accessHistory, pattern)
	if len(p.accessHistory) > 1000 {
		p.accessHistory = p.accessHistory[len(p.accessHistory)-1000:]
	}

	if fromFile != "" && fromFile != path {
		p.updateCoAccess(fromFile, path)
	}

	if fromFile != "" {
		fromExt := filepath.Ext(fromFile)
		toExt := filepath.Ext(path)
		if fromExt != "" && toExt != "" {
			p.typeRelations[fromExt+"|"+toExt]++
		}
	}
}

// PredictFiles predicts which files might be needed based on the current file.
func (p *Predictor) PredictFiles(currentFile string, limit int) []PredictedFile {
	p.mu.RLock()
	defer p.mu.RUnlock()

	predictions := make(map[string]float64)

	// Co-accessed files
	for key, entry := range p.coAccess {
		if strings.Contains(key, currentFile) {
			other := entry.FileA
			if other == currentFile {
				other = entry.FileB
			}
			predictions[other] += entry.Strength * 0.5
		}
	}

	// Files of related types
	currentExt := filepath.Ext(currentFile)
	if currentExt != "" {
		for key, count := range p.typeRelations {
			parts := strings.Split(key, "|")
			if len(parts) == 2 && parts[0] == currentExt {
				for _, access := range p.accessHistory {
					if filepath.Ext(access.Path) == parts[1] {
						predictions[access.Path] += float64(count) * 0.01
					}
				}
			}
		}
	}

	// Import graph
	if imports, ok := p.importGraph[currentFile]; ok {
		for _, imp := range imports {
			predictions[imp] += 0.4
		}
	}

	// Same-directory files
	currentDir := filepath.Dir(currentFile)
	for _, access := range p.accessHistory {
		if filepath.Dir(access.Path) == currentDir && access.Path != currentFile {
			predictions[access.Path] += 0.1
		}
	}

	// Convert to sorted list
	var result []PredictedFile
	for path, score := range predictions {
		if _, err := os.Stat(path); err == nil {
			result = append(result, PredictedFile{
				Path:       path,
				Confidence: score / (score + 1), // Normalize to 0-1
				Reason:     p.getPredictionReason(path, currentFile),
			})
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].Confidence > result[j].Confidence
	})

	if len(result) > limit {
		result = result[:limit]
	}

	return result
}

// LearnImports parses a file to learn its import relationships.
func (p *Predictor) LearnImports(filePath string) {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return
	}

	ext := filepath.Ext(filePath)
	var imports []string

	switch ext {
	case ".go":
		imports = parseGoImports(string(content), filepath.Dir(filePath))
	case ".ts", ".tsx", ".js", ".jsx":
		imports = parseJSImports(string(content), filepath.Dir(filePath))
	case ".py":
		imports = parsePythonImports(string(content), filepath.Dir(filePath))
	}

	if len(imports) > 0 {
		p.mu.Lock()
		p.importGraph[filePath] = imports
		p.mu.Unlock()
	}
}

// Clear removes all learned patterns.
func (p *Predictor) Clear() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.accessHistory = make([]FileAccessPattern, 0)
	p.coAccess = make(map[string]*CoAccessEntry)
	p.typeRelations = make(map[string]int)
	p.importGraph = make(map[string][]string)
}

// --- internal ---

func (p *Predictor) updateCoAccess(fileA, fileB string) {
	if fileA > fileB {
		fileA, fileB = fileB, fileA
	}
	key := fileA + "|" + fileB

	entry, ok := p.coAccess[key]
	if !ok {
		entry = &CoAccessEntry{FileA: fileA, FileB: fileB}
		p.coAccess[key] = entry
	}

	entry.Count++
	entry.LastSeen = time.Now()

	baseStrength := 0.3 + 0.7*(1-1/float64(entry.Count+1))
	daysSince := time.Since(entry.LastSeen).Hours() / 24
	decay := 1.0
	if daysSince > 0 {
		decay = 1.0 / (1.0 + daysSince/30.0)
	}
	entry.Strength = baseStrength * decay
}

func (p *Predictor) getPredictionReason(predicted, current string) string {
	fileA, fileB := current, predicted
	if fileA > fileB {
		fileA, fileB = fileB, fileA
	}
	if entry, ok := p.coAccess[fileA+"|"+fileB]; ok && entry.Count > 2 {
		return "frequently accessed together"
	}
	if imports, ok := p.importGraph[current]; ok {
		for _, imp := range imports {
			if imp == predicted {
				return "imported by current file"
			}
		}
	}
	if filepath.Dir(predicted) == filepath.Dir(current) {
		return "same directory"
	}
	return "pattern match"
}

func parseGoImports(content, baseDir string) []string {
	var imports []string
	importRe := regexp.MustCompile(`import\s+(?:\(\s*([\s\S]*?)\s*\)|"([^"]+)")`)
	for _, match := range importRe.FindAllStringSubmatch(content, -1) {
		if match[1] != "" {
			lineRe := regexp.MustCompile(`"([^"]+)"`)
			for _, lm := range lineRe.FindAllStringSubmatch(match[1], -1) {
				imports = append(imports, lm[1])
			}
		} else if match[2] != "" {
			imports = append(imports, match[2])
		}
	}
	return imports
}

func parseJSImports(content, baseDir string) []string {
	var imports []string
	importRe := regexp.MustCompile(`import\s+(?:.*?\s+from\s+)?['"]([^'"]+)['"]`)
	for _, match := range importRe.FindAllStringSubmatch(content, -1) {
		path := match[1]
		if strings.HasPrefix(path, "./") || strings.HasPrefix(path, "../") {
			absPath := filepath.Join(baseDir, path)
			for _, ext := range []string{"", ".ts", ".tsx", ".js", ".jsx"} {
				if _, err := os.Stat(absPath + ext); err == nil {
					imports = append(imports, absPath+ext)
					break
				}
			}
		}
	}
	return imports
}

func parsePythonImports(content, baseDir string) []string {
	var imports []string
	importRe := regexp.MustCompile(`(?:from\s+(\S+)\s+import|import\s+(\S+))`)
	for _, match := range importRe.FindAllStringSubmatch(content, -1) {
		module := match[1]
		if module == "" {
			module = match[2]
		}
		if strings.HasPrefix(module, ".") {
			relPath := strings.ReplaceAll(module, ".", string(filepath.Separator))
			absPath := filepath.Join(baseDir, relPath+".py")
			if _, err := os.Stat(absPath); err == nil {
				imports = append(imports, absPath)
			}
		}
	}
	return imports
}
