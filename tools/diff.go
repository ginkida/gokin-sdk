package tools

import (
	"context"
	"fmt"
	"os"
	"strings"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// DiffTool compares files or content and produces unified diffs.
type DiffTool struct{}

// NewDiff creates a new DiffTool.
func NewDiff() *DiffTool {
	return &DiffTool{}
}

func (t *DiffTool) Name() string { return "diff" }

func (t *DiffTool) Description() string {
	return "Compares two files or a file and content string. Returns a unified diff."
}

func (t *DiffTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"file1": {
					Type:        genai.TypeString,
					Description: "Path to the first file",
				},
				"file2": {
					Type:        genai.TypeString,
					Description: "Path to the second file (provide this or content)",
				},
				"content": {
					Type:        genai.TypeString,
					Description: "Content string to compare against file1 (alternative to file2)",
				},
				"context_lines": {
					Type:        genai.TypeInteger,
					Description: "Lines of context around changes (default: 3)",
				},
			},
			Required: []string{"file1"},
		},
	}
}

func (t *DiffTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	file1, ok := sdk.GetString(args, "file1")
	if !ok || file1 == "" {
		return sdk.NewErrorResult("file1 is required"), nil
	}

	file2, _ := sdk.GetString(args, "file2")
	content, _ := sdk.GetString(args, "content")
	contextLines := sdk.GetIntDefault(args, "context_lines", 3)

	if file2 == "" && content == "" {
		return sdk.NewErrorResult("either file2 or content is required"), nil
	}

	data1, err := os.ReadFile(file1)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("error reading file1: %s", err)), nil
	}

	var data2 []byte
	label2 := "content"
	if file2 != "" {
		data2, err = os.ReadFile(file2)
		if err != nil {
			return sdk.NewErrorResult(fmt.Sprintf("error reading file2: %s", err)), nil
		}
		label2 = file2
	} else {
		data2 = []byte(content)
	}

	lines1 := strings.Split(string(data1), "\n")
	lines2 := strings.Split(string(data2), "\n")

	diff := unifiedDiff(file1, label2, lines1, lines2, contextLines)
	if diff == "" {
		return sdk.NewSuccessResult("Files are identical"), nil
	}

	return sdk.NewSuccessResult(diff), nil
}

// unifiedDiff produces a unified diff between two sets of lines.
func unifiedDiff(label1, label2 string, a, b []string, contextLines int) string {
	// Find longest common subsequence using dynamic programming
	m, n := len(a), len(b)
	dp := make([][]int, m+1)
	for i := range dp {
		dp[i] = make([]int, n+1)
	}
	for i := m - 1; i >= 0; i-- {
		for j := n - 1; j >= 0; j-- {
			if a[i] == b[j] {
				dp[i][j] = dp[i+1][j+1] + 1
			} else if dp[i+1][j] > dp[i][j+1] {
				dp[i][j] = dp[i+1][j]
			} else {
				dp[i][j] = dp[i][j+1]
			}
		}
	}

	// Generate edit operations
	type editOp struct {
		kind byte // '=', '-', '+'
		line string
		aIdx int
		bIdx int
	}

	var ops []editOp
	i, j := 0, 0
	for i < m || j < n {
		if i < m && j < n && a[i] == b[j] {
			ops = append(ops, editOp{'=', a[i], i, j})
			i++
			j++
		} else if j < n && (i >= m || dp[i][j+1] >= dp[i+1][j]) {
			ops = append(ops, editOp{'+', b[j], i, j})
			j++
		} else if i < m {
			ops = append(ops, editOp{'-', a[i], i, j})
			i++
		}
	}

	// Check if there are any changes
	hasChanges := false
	for _, op := range ops {
		if op.kind != '=' {
			hasChanges = true
			break
		}
	}
	if !hasChanges {
		return ""
	}

	// Build unified diff with context
	var builder strings.Builder
	builder.WriteString(fmt.Sprintf("--- %s\n", label1))
	builder.WriteString(fmt.Sprintf("+++ %s\n", label2))

	// Group operations into hunks
	type hunk struct {
		startA, countA int
		startB, countB int
		lines          []string
	}

	var hunks []hunk
	inHunk := false
	var currentHunk hunk
	contextBefore := make([]editOp, 0)

	for idx, op := range ops {
		if op.kind != '=' {
			if !inHunk {
				// Start a new hunk with context before
				startCtx := len(contextBefore) - contextLines
				if startCtx < 0 {
					startCtx = 0
				}
				currentHunk = hunk{}
				if len(contextBefore) > 0 {
					for _, c := range contextBefore[startCtx:] {
						currentHunk.lines = append(currentHunk.lines, " "+c.line)
						currentHunk.countA++
						currentHunk.countB++
					}
					currentHunk.startA = contextBefore[startCtx].aIdx + 1
					currentHunk.startB = contextBefore[startCtx].bIdx + 1
				} else {
					currentHunk.startA = op.aIdx + 1
					currentHunk.startB = op.bIdx + 1
				}
				inHunk = true
			}

			switch op.kind {
			case '-':
				currentHunk.lines = append(currentHunk.lines, "-"+op.line)
				currentHunk.countA++
			case '+':
				currentHunk.lines = append(currentHunk.lines, "+"+op.line)
				currentHunk.countB++
			}
			contextBefore = contextBefore[:0]
		} else {
			if inHunk {
				// Add context after
				currentHunk.lines = append(currentHunk.lines, " "+op.line)
				currentHunk.countA++
				currentHunk.countB++

				// Check if we've added enough trailing context
				trailingCtx := 0
				for k := len(currentHunk.lines) - 1; k >= 0; k-- {
					if currentHunk.lines[k][0] == ' ' {
						trailingCtx++
					} else {
						break
					}
				}

				// Check if next change is within context range
				nextChangeIdx := -1
				for k := idx + 1; k < len(ops); k++ {
					if ops[k].kind != '=' {
						nextChangeIdx = k
						break
					}
				}

				if trailingCtx >= contextLines && (nextChangeIdx == -1 || nextChangeIdx-idx > 2*contextLines) {
					hunks = append(hunks, currentHunk)
					inHunk = false
					contextBefore = contextBefore[:0]
				}
			} else {
				contextBefore = append(contextBefore, op)
			}
		}
	}

	if inHunk {
		hunks = append(hunks, currentHunk)
	}

	for _, h := range hunks {
		builder.WriteString(fmt.Sprintf("@@ -%d,%d +%d,%d @@\n", h.startA, h.countA, h.startB, h.countB))
		for _, line := range h.lines {
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}

	return builder.String()
}
