package context

// SummaryStrategy defines how summarization behaves.
type SummaryStrategy struct {
	Name             string
	MaxTokens        int
	KeepRecentCount  int
	PreserveErrors   bool
	PreserveDecisions bool
	PromptTemplate   string
}

// DefaultStrategy returns the default summarization strategy.
func DefaultStrategy() *SummaryStrategy {
	return &SummaryStrategy{
		Name:             "default",
		MaxTokens:        2000,
		KeepRecentCount:  10,
		PreserveErrors:   true,
		PreserveDecisions: true,
		PromptTemplate: `Summarize the following conversation context concisely.
Preserve:
- Key decisions made
- Errors encountered and how they were resolved
- Important file paths and changes
- Current state of the task

Context to summarize:
%s`,
	}
}

// CompactStrategy returns an aggressive compression strategy.
func CompactStrategy() *SummaryStrategy {
	return &SummaryStrategy{
		Name:             "compact",
		MaxTokens:        1000,
		KeepRecentCount:  5,
		PreserveErrors:   true,
		PreserveDecisions: false,
		PromptTemplate: `Create a very brief summary of the key facts from this conversation.
Focus on: what was done, what files were changed, and any unresolved issues.
Be extremely concise (under 500 words).

Context:
%s`,
	}
}

// VerboseStrategy returns a strategy that preserves more context.
func VerboseStrategy() *SummaryStrategy {
	return &SummaryStrategy{
		Name:             "verbose",
		MaxTokens:        4000,
		KeepRecentCount:  15,
		PreserveErrors:   true,
		PreserveDecisions: true,
		PromptTemplate: `Provide a detailed summary of this conversation maintaining all important context.
Include:
- All decisions and their reasoning
- Every error and its resolution
- All file paths and changes with details
- The current state and next steps
- Any user preferences or constraints mentioned

Context:
%s`,
	}
}
