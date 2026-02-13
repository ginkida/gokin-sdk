package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// SearchResult represents a single search result.
type SearchResult struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Snippet string `json:"snippet"`
}

// WebSearchTool searches the web using SerpAPI or Google Custom Search.
type WebSearchTool struct {
	apiKey   string
	provider string // "serpapi" or "google"
	googleCX string
	client   *http.Client
}

// NewWebSearch creates a new WebSearchTool with SerpAPI.
func NewWebSearch(apiKey string) *WebSearchTool {
	return &WebSearchTool{
		apiKey:   apiKey,
		provider: "serpapi",
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// NewWebSearchGoogle creates a new WebSearchTool with Google Custom Search.
func NewWebSearchGoogle(apiKey, cx string) *WebSearchTool {
	return &WebSearchTool{
		apiKey:   apiKey,
		provider: "google",
		googleCX: cx,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func (t *WebSearchTool) Name() string { return "web_search" }

func (t *WebSearchTool) Description() string {
	return "Searches the web and returns results with titles, URLs, and snippets."
}

func (t *WebSearchTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"query": {
					Type:        genai.TypeString,
					Description: "The search query",
				},
				"num_results": {
					Type:        genai.TypeInteger,
					Description: "Number of results (default: 5, max: 10)",
				},
			},
			Required: []string{"query"},
		},
	}
}

func (t *WebSearchTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	query, ok := sdk.GetString(args, "query")
	if !ok || query == "" {
		return sdk.NewErrorResult("query is required"), nil
	}

	if t.apiKey == "" {
		return sdk.NewErrorResult("search API key is not configured"), nil
	}

	numResults := sdk.GetIntDefault(args, "num_results", 5)
	if numResults < 1 {
		numResults = 1
	}
	if numResults > 10 {
		numResults = 10
	}

	var results []SearchResult
	var err error

	switch t.provider {
	case "google":
		results, err = t.searchGoogle(ctx, query, numResults)
	default:
		results, err = t.searchSerpAPI(ctx, query, numResults)
	}

	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("search failed: %s", err)), nil
	}

	if len(results) == 0 {
		return sdk.NewSuccessResult("No results found."), nil
	}

	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("Search results for: %s\n\n", query))
	for i, r := range results {
		builder.WriteString(fmt.Sprintf("%d. %s\n   %s\n   %s\n\n", i+1, r.Title, r.URL, r.Snippet))
	}

	return sdk.NewSuccessResult(builder.String()), nil
}

func (t *WebSearchTool) searchSerpAPI(ctx context.Context, query string, num int) ([]SearchResult, error) {
	u := fmt.Sprintf("https://serpapi.com/search?engine=google&q=%s&num=%d&api_key=%s",
		url.QueryEscape(query), num, t.apiKey)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var data struct {
		OrganicResults []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"organic_results"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("error parsing response: %w", err)
	}

	var results []SearchResult
	for _, r := range data.OrganicResults {
		results = append(results, SearchResult{
			Title:   r.Title,
			URL:     r.Link,
			Snippet: r.Snippet,
		})
	}

	return results, nil
}

func (t *WebSearchTool) searchGoogle(ctx context.Context, query string, num int) ([]SearchResult, error) {
	u := fmt.Sprintf("https://www.googleapis.com/customsearch/v1?q=%s&num=%d&cx=%s&key=%s",
		url.QueryEscape(query), num, t.googleCX, t.apiKey)

	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return nil, err
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("API returned status %d", resp.StatusCode)
	}

	var data struct {
		Items []struct {
			Title   string `json:"title"`
			Link    string `json:"link"`
			Snippet string `json:"snippet"`
		} `json:"items"`
	}

	if err := json.Unmarshal(body, &data); err != nil {
		return nil, fmt.Errorf("error parsing response: %w", err)
	}

	var results []SearchResult
	for _, item := range data.Items {
		results = append(results, SearchResult{
			Title:   item.Title,
			URL:     item.Link,
			Snippet: item.Snippet,
		})
	}

	return results, nil
}
