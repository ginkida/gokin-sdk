package tools

import (
	"context"
	"fmt"
	"strings"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// SemanticSearchResult represents a single semantic search result.
type SemanticSearchResult struct {
	FilePath string  `json:"file_path"`
	Content  string  `json:"content"`
	Score    float64 `json:"score"`
	Line     int     `json:"line"`
}

// SemanticSearcher performs semantic code search.
type SemanticSearcher interface {
	Search(ctx context.Context, query string, limit int) ([]SemanticSearchResult, error)
}

// SemanticSearchTool searches the codebase using semantic similarity.
type SemanticSearchTool struct {
	searcher SemanticSearcher
}

// NewSemanticSearch creates a new SemanticSearchTool.
func NewSemanticSearch() *SemanticSearchTool {
	return &SemanticSearchTool{}
}

// SetSearcher sets the semantic searcher.
func (t *SemanticSearchTool) SetSearcher(searcher SemanticSearcher) {
	t.searcher = searcher
}

func (t *SemanticSearchTool) Name() string        { return "semantic_search" }
func (t *SemanticSearchTool) Description() string { return "Search the codebase using semantic similarity to find relevant code." }

func (t *SemanticSearchTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"query": {
					Type:        genai.TypeString,
					Description: "Natural language query describing what you're looking for",
				},
				"top_k": {
					Type:        genai.TypeInteger,
					Description: "Maximum number of results to return (default: 10)",
				},
			},
			Required: []string{"query"},
		},
	}
}

func (t *SemanticSearchTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	if t.searcher == nil {
		return sdk.NewErrorResult("semantic_search: no searcher configured"), nil
	}

	query, ok := sdk.GetString(args, "query")
	if !ok || query == "" {
		return sdk.NewErrorResult("query is required"), nil
	}

	topK := sdk.GetIntDefault(args, "top_k", 10)
	if topK <= 0 {
		topK = 10
	}

	results, err := t.searcher.Search(ctx, query, topK)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("search failed: %s", err)), nil
	}

	if len(results) == 0 {
		return sdk.NewSuccessResult("No results found."), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d results:\n\n", len(results)))
	for i, r := range results {
		sb.WriteString(fmt.Sprintf("%d. %s", i+1, r.FilePath))
		if r.Line > 0 {
			sb.WriteString(fmt.Sprintf(":%d", r.Line))
		}
		sb.WriteString(fmt.Sprintf(" (score: %.3f)\n", r.Score))
		if r.Content != "" {
			sb.WriteString(fmt.Sprintf("   %s\n", r.Content))
		}
	}

	return sdk.NewSuccessResult(sb.String()), nil
}
