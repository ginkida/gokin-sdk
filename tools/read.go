package tools

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	sdk "github.com/ginkida/gokin-sdk"

	"google.golang.org/genai"
)

// ReadTool reads files and returns their contents with line numbers.
type ReadTool struct{}

// NewRead creates a new ReadTool.
func NewRead() *ReadTool {
	return &ReadTool{}
}

func (t *ReadTool) Name() string { return "read" }

func (t *ReadTool) Description() string {
	return "Reads a file and returns its contents with line numbers."
}

func (t *ReadTool) Declaration() *genai.FunctionDeclaration {
	return &genai.FunctionDeclaration{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: &genai.Schema{
			Type: genai.TypeObject,
			Properties: map[string]*genai.Schema{
				"file_path": {
					Type:        genai.TypeString,
					Description: "The path to the file to read",
				},
				"offset": {
					Type:        genai.TypeInteger,
					Description: "Line number to start reading from (1-indexed)",
				},
				"limit": {
					Type:        genai.TypeInteger,
					Description: "Maximum number of lines to read (default: 2000)",
				},
			},
			Required: []string{"file_path"},
		},
	}
}

func (t *ReadTool) Execute(ctx context.Context, args map[string]any) (*sdk.ToolResult, error) {
	filePath, ok := sdk.GetString(args, "file_path")
	if !ok || filePath == "" {
		return sdk.NewErrorResult("file_path is required"), nil
	}

	offset := sdk.GetIntDefault(args, "offset", 1)
	limit := sdk.GetIntDefault(args, "limit", 2000)

	if offset < 1 {
		offset = 1
	}

	info, err := os.Stat(filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return sdk.NewErrorResult(fmt.Sprintf("file not found: %s", filePath)), nil
		}
		return sdk.NewErrorResult(fmt.Sprintf("error accessing file: %s", err)), nil
	}
	if info.IsDir() {
		return sdk.NewErrorResult(fmt.Sprintf("%s is a directory, not a file", filePath)), nil
	}

	file, err := os.Open(filePath)
	if err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("error opening file: %s", err)), nil
	}
	defer file.Close()

	var builder strings.Builder
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	lineNum := 0
	linesRead := 0
	const maxLineLen = 2000

	for scanner.Scan() {
		lineNum++

		if lineNum < offset {
			continue
		}
		if linesRead >= limit {
			break
		}

		line := scanner.Text()
		if len(line) > maxLineLen {
			line = line[:maxLineLen] + "..."
		}

		builder.WriteString(fmt.Sprintf("%6d\t%s\n", lineNum, line))
		linesRead++
	}

	if err := scanner.Err(); err != nil {
		return sdk.NewErrorResult(fmt.Sprintf("error reading file: %s", err)), nil
	}

	content := builder.String()
	if content == "" {
		if offset > 1 && lineNum > 0 {
			content = fmt.Sprintf("(offset %d is beyond end of file — file has %d lines)", offset, lineNum)
		} else {
			content = "(empty file)"
		}
	}

	return sdk.NewSuccessResult(content), nil
}
