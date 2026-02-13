package tools

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// WebFetchTool fetches content from URLs and converts HTML to readable text.
type WebFetchTool struct {
	client *http.Client
}

// NewWebFetch creates a new WebFetchTool.
func NewWebFetch() *WebFetchTool {
	return &WebFetchTool{
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (t *WebFetchTool) Name() string { return "web_fetch" }

func (t *WebFetchTool) Description() string {
	return "Fetches content from a URL. HTML is converted to readable text."
}

func (t *WebFetchTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"url": {
					Type:        genai.TypeString,
					Description: "The URL to fetch",
				},
			},
			Required: []string{"url"},
		},
	}
}

func (t *WebFetchTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	rawURL, ok := sdk.GetString(args, "url")
	if !ok || rawURL == "" {
		return sdk.NewErrorResult("url is required"), nil
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("invalid URL: %s", err)), nil
	}

	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return sdk.NewErrorResult("only http and https URLs are supported"), nil
	}

	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("error creating request: %s", err)), nil
	}

	req.Header.Set("User-Agent", "gokin-sdk/0.2.0")
	req.Header.Set("Accept", "text/html,application/xhtml+xml,text/plain,application/json")

	resp, err := t.client.Do(req)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("error fetching URL: %s", err)), nil
	}
	defer resp.Body.Close()

	// Limit response size to 1MB
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024))
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("error reading response: %s", err)), nil
	}

	if resp.StatusCode >= 400 {
		return sdk.NewErrorResult(fmt.Sprintf("HTTP %d: %s", resp.StatusCode, string(body[:min(500, len(body))]))), nil
	}

	content := string(body)
	contentType := resp.Header.Get("Content-Type")

	// Convert HTML to text
	if strings.Contains(contentType, "html") {
		content = htmlToText(content)
	}

	// Truncate if needed
	const maxLen = 50000
	if len(content) > maxLen {
		content = content[:maxLen] + "\n... (content truncated)"
	}

	result := fmt.Sprintf("URL: %s\nStatus: %d\nContent-Type: %s\nLength: %d\n\n%s",
		rawURL, resp.StatusCode, contentType, len(body), content)

	return sdk.NewSuccessResult(result), nil
}

// htmlToText converts HTML to readable plain text.
func htmlToText(html string) string {
	var builder strings.Builder
	inTag := false
	inScript := false
	inStyle := false
	tagName := ""
	var currentTag strings.Builder

	for i := 0; i < len(html); i++ {
		ch := html[i]

		if ch == '<' {
			inTag = true
			currentTag.Reset()
			continue
		}

		if ch == '>' && inTag {
			inTag = false
			tag := currentTag.String()
			lower := strings.ToLower(tag)

			// Extract tag name
			parts := strings.Fields(lower)
			if len(parts) > 0 {
				tagName = parts[0]
			}

			switch {
			case tagName == "script":
				inScript = true
			case tagName == "/script":
				inScript = false
			case tagName == "style":
				inStyle = true
			case tagName == "/style":
				inStyle = false
			case tagName == "br" || tagName == "br/":
				builder.WriteString("\n")
			case tagName == "/p" || tagName == "/div" || tagName == "/h1" || tagName == "/h2" || tagName == "/h3" || tagName == "/h4" || tagName == "/h5" || tagName == "/h6":
				builder.WriteString("\n\n")
			case tagName == "/li":
				builder.WriteString("\n")
			case tagName == "li":
				builder.WriteString("  - ")
			case strings.HasPrefix(tagName, "h") && len(tagName) == 2 && tagName[1] >= '1' && tagName[1] <= '6':
				builder.WriteString("\n## ")
			}
			continue
		}

		if inTag {
			currentTag.WriteByte(ch)
			continue
		}

		if inScript || inStyle {
			continue
		}

		// Decode common entities
		if ch == '&' && i+1 < len(html) {
			end := strings.IndexByte(html[i:], ';')
			if end > 0 && end < 10 {
				entity := html[i : i+end+1]
				switch entity {
				case "&amp;":
					builder.WriteByte('&')
				case "&lt;":
					builder.WriteByte('<')
				case "&gt;":
					builder.WriteByte('>')
				case "&quot;":
					builder.WriteByte('"')
				case "&apos;":
					builder.WriteByte('\'')
				case "&nbsp;":
					builder.WriteByte(' ')
				default:
					builder.WriteString(entity)
				}
				i += end
				continue
			}
		}

		builder.WriteByte(ch)
	}

	// Clean up whitespace
	result := builder.String()
	lines := strings.Split(result, "\n")
	var cleaned []string
	emptyCount := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			emptyCount++
			if emptyCount <= 2 {
				cleaned = append(cleaned, "")
			}
		} else {
			emptyCount = 0
			cleaned = append(cleaned, trimmed)
		}
	}

	return strings.TrimSpace(strings.Join(cleaned, "\n"))
}
