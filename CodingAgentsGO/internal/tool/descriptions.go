package tool

import "strings"

func PromptReference(toolNames []string) string {
	available := make(map[string]bool, len(toolNames))
	for _, name := range toolNames {
		available[name] = true
	}

	var b strings.Builder
	if available["explorer"] {
		b.WriteString("- explorer: Actions: list, read, tree, search, outline, dependencies. Args: {\"action\":\"...\",\"path\":\"...\",\"query\":\"...\"}.\n")
	}
	if available["writer"] {
		b.WriteString("- writer: Actions: write, append, patch, patch_block, fmt, mkdir, merge. Args: {\"action\":\"...\",\"path\":\"...\",\"content\":\"...\",\"target\":\"...\",\"replacement\":\"...\",\"lines\":[\"...\"]}.\n")
	}
	if available["coderunner"] {
		b.WriteString("- coderunner: Execute short local snippets. Args: {\"code\":\"...\",\"language\":\"go|python|js\",\"timeout_seconds\":15}.\n")
	}
	if available["dockerrunner"] {
		b.WriteString("- dockerrunner: Execute privileged or isolated commands with approval. Args: {\"command\":\"...\",\"language\":\"go|python|js\",\"image\":\"optional\"}.\n")
	}
	if available["memory_retriever"] {
		b.WriteString("- memory_retriever: Query semantic memory of prior runs and summaries. Args: {\"query\":\"...\"}.\n")
	}
	if available["codebase_search"] {
		b.WriteString("- codebase_search: Query indexed code snippets by semantic topic or symbol. Args: {\"query\":\"...\"}.\n")
	}
	return b.String()
}
