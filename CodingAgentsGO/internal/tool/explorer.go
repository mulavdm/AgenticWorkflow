package tool

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// FileExplorer allows agents to list files and read contents to understand the codebase.
type FileExplorer struct {
	BasePath string
}

func (f *FileExplorer) SetBasePath(path string) {
	f.BasePath = path
}

func (f *FileExplorer) Name() string {
	return "explorer"
}

func (f *FileExplorer) Description() string {
	return "Explores the codebase. Actions: list (single dir), read (file content), tree (recursive list), search (find text in files), outline (parse Go declarations/structs/functions), dependencies (list Go package imports). Args: {\"action\": \"list|read|tree|search|outline|dependencies\", \"path\": \"relative/path\", \"query\": \"optional search term\"}"
}

func (f *FileExplorer) Execute(ctx context.Context, input string) (string, error) {
	var args struct {
		Action string `json:"action"`
		Path   string `json:"path"`
		Query  string `json:"query"`
	}

	if err := json.Unmarshal([]byte(input), &args); err != nil {
		// Fallback for very simple manual parsing if JSON is slightly broken
		if strings.Contains(input, "\"read\"") {
			args.Action = "read"
		} else if strings.Contains(input, "\"search\"") {
			args.Action = "search"
		} else if strings.Contains(input, "\"tree\"") {
			args.Action = "tree"
		} else {
			args.Action = "list"
		}

		if strings.Contains(input, "\"path\":") {
			parts := strings.Split(input, "\"path\":")
			if len(parts) > 1 {
				p := strings.TrimSpace(parts[1])
				p = strings.Trim(p, "\",} ")
				args.Path = p
			}
		}
		if strings.Contains(input, "\"query\":") {
			parts := strings.Split(input, "\"query\":")
			if len(parts) > 1 {
				p := strings.TrimSpace(parts[1])
				p = strings.Trim(p, "\",} ")
				args.Query = p
			}
		}
	}

	if args.Path == "" {
		args.Path = "."
	}
	if args.Action == "" {
		args.Action = "list"
	}

	// Clean and validate path to prevent traversal
	path := args.Path
	// Auto-correct leading slashes to support agents that default to absolute-style paths
	if strings.HasPrefix(path, "/") || strings.HasPrefix(path, "\\") {
		path = strings.TrimLeft(path, "/\\")
	}

	path = filepath.Clean(path)

	// Check if it's an absolute path that's actually within our workspace
	if filepath.IsAbs(path) && f.BasePath != "" {
		rel, err := filepath.Rel(f.BasePath, path)
		if err == nil && !strings.HasPrefix(rel, "..") {
			path = rel // Convert to relative for internal use
		}
	}

	if strings.HasPrefix(path, "..") || filepath.IsAbs(path) {
		return "", fmt.Errorf("security violation: path %s is outside the allowed workspace", args.Path)
	}

	// Apply base path if set
	if f.BasePath != "" {
		path = filepath.Join(f.BasePath, path)
	}

	hasGlob := strings.Contains(path, "*") || strings.Contains(path, "?")

	// Pre-check for existence to provide smart hints
	if !hasGlob && path != f.BasePath {
		if _, err := os.Stat(path); os.IsNotExist(err) {
			// SMART HINT: Try to find where this file/dir actually is
			base := filepath.Base(path)
			var suggestions []string
			walkRoot := "."
			if f.BasePath != "" {
				walkRoot = f.BasePath
			}
			filepath.Walk(walkRoot, func(p string, info os.FileInfo, err error) error {
				if err == nil && p != path {
					// Check for filename match OR similar directory name
					if info.Name() == base || (info.IsDir() && strings.Contains(strings.ToLower(info.Name()), strings.ToLower(base))) {
						rel, _ := filepath.Rel(walkRoot, p)
						suggestions = append(suggestions, rel)
					}
				}
				return nil
			})
			if len(suggestions) > 0 {
				// Limit suggestions
				if len(suggestions) > 3 {
					suggestions = suggestions[:3]
				}
				return "", fmt.Errorf("path not found: %s. Did you mean: %s? ALWAYS use the exact relative path from the root.", args.Path, strings.Join(suggestions, " or "))
			}
		}
	}

	action := args.Action

	switch action {
	case "list":
		if hasGlob {
			matches, err := filepath.Glob(path)
			if err != nil {
				return "", fmt.Errorf("failed to expand glob pattern %s: %w", args.Path, err)
			}
			var sb strings.Builder
			sb.WriteString(fmt.Sprintf("Glob listing for %s:\n", args.Path))
			for _, match := range matches {
				info, err := os.Stat(match)
				if err != nil {
					continue
				}
				if info.IsDir() {
					sb.WriteString(fmt.Sprintf("[DIR]  %s/\n", filepath.Base(match)))
				} else {
					sb.WriteString(fmt.Sprintf("[FILE] %s (%d bytes)\n", filepath.Base(match), info.Size()))
				}
			}
			return sb.String(), nil
		}

		files, err := os.ReadDir(path)
		if err != nil {
			// Check if it's a file and provide a helpful hint
			if info, statErr := os.Stat(path); statErr == nil && !info.IsDir() {
				return "", fmt.Errorf("failed to list directory %s: it is a file. Did you mean to use action: 'read'?", path)
			}
			return "", fmt.Errorf("failed to list directory %s: %w", path, err)
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Directory listing for %s:\n", path))
		for _, file := range files {
			info, _ := file.Info()
			if file.IsDir() {
				sb.WriteString(fmt.Sprintf("[DIR]  %s/\n", file.Name()))
			} else {
				sb.WriteString(fmt.Sprintf("[FILE] %s (%d bytes)\n", file.Name(), info.Size()))
			}
		}
		return sb.String(), nil

	case "read":
		if args.Path == "" || args.Path == "." {
			return "", fmt.Errorf("read action requires a file path")
		}

		var startLine, endLine int
		var rangeArgs struct {
			StartLine int `json:"start_line"`
			EndLine   int `json:"end_line"`
		}
		if err := json.Unmarshal([]byte(input), &rangeArgs); err == nil {
			startLine = rangeArgs.StartLine
			endLine = rangeArgs.EndLine
		}

		if hasGlob {
			matches, err := filepath.Glob(path)
			if err != nil || len(matches) == 0 {
				return "", fmt.Errorf("no files matched glob pattern %s", args.Path)
			}
			var sb strings.Builder
			for _, match := range matches {
				info, err := os.Stat(match)
				if err != nil || info.IsDir() {
					continue
				}
				content, err := os.ReadFile(match)
				if err != nil {
					continue
				}
				sb.WriteString(fmt.Sprintf("\n--- %s ---\n", filepath.ToSlash(match)))
				// For glob read, we don't support range reading for simplicity here
				if len(content) > 5000 {
					sb.WriteString(string(content[:5000]) + "\n... [TRUNCATED]\n")
				} else {
					sb.WriteString(string(content) + "\n")
				}
			}
			return strings.TrimSpace(sb.String()), nil
		}

		file, err := os.Open(path)
		if err != nil {
			return "", fmt.Errorf("failed to open file %s: %w", path, err)
		}
		defer file.Close()

		var sb strings.Builder
		scanner := bufio.NewScanner(file)
		lineNum := 1
		for scanner.Scan() {
			if startLine > 0 && lineNum < startLine {
				lineNum++
				continue
			}
			if endLine > 0 && lineNum > endLine {
				break
			}
			sb.WriteString(fmt.Sprintf("%4d | %s\n", lineNum, scanner.Text()))
			lineNum++

			// Safety limit for context window if range is too large or not specified
			if endLine == 0 && lineNum > 500 {
				sb.WriteString("... [TRUNCATED (max 500 lines, use start_line/end_line for more)]")
				break
			}
		}

		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("error reading file: %w", err)
		}

		return sb.String(), nil

	case "tree":
		walkPath := path
		if hasGlob {
			walkPath = filepath.Dir(path)
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Recursive listing for %s:\n", walkPath))
		count := 0
		err := filepath.Walk(walkPath, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(walkPath, p)
			if rel == "." {
				return nil
			}

			if count >= 400 {
				return fmt.Errorf("LIMIT_REACHED")
			}

			if info.IsDir() {
				sb.WriteString(fmt.Sprintf("[DIR]  %s/\n", rel))
			} else {
				sb.WriteString(fmt.Sprintf("[FILE] %s\n", rel))
			}
			count++

			// After listing the directory itself, decide if we should skip its children
			if info.IsDir() && p != walkPath {
				name := info.Name()
				if strings.HasPrefix(name, ".") || name == "vendor" || name == "node_modules" || name == "logs" || name == "scratch" {
					return filepath.SkipDir
				}
			}
			return nil
		})
		if err != nil && err.Error() != "LIMIT_REACHED" {
			return "", fmt.Errorf("failed to walk tree %s: %w", path, err)
		}
		if err != nil && err.Error() == "LIMIT_REACHED" {
			sb.WriteString("\n... [TRUNCATED: too many files. To prevent context overflow, listing is capped at 400 entries. Use 'explorer:list' to explore subdirectories individually.]")
		}
		return sb.String(), nil

	case "search":
		if args.Query == "" {
			return "", fmt.Errorf("search action requires a 'query' argument")
		}

		// Sanitize query (trim accidental quotes from agents struggling with JSON)
		args.Query = strings.Trim(args.Query, "\"'` ")
		if args.Query == "" {
			return "", fmt.Errorf("search action requires a non-empty 'query' argument")
		}

		walkRoot := "."
		if f.BasePath != "" {
			walkRoot = f.BasePath
		}

		walkPath := path
		if hasGlob {
			walkPath = filepath.Dir(path)
		}

		// 1. Split query into multiple terms if comma-separated (supporting agents that pass lists)
		queries := strings.Split(args.Query, ",")
		var regexes []*regexp.Regexp

		for _, q := range queries {
			q = strings.TrimSpace(q)
			if q == "" {
				continue
			}
			// 2. Convert glob-like patterns to Regex (e.g. *.go -> .*\.go$)
			processed := q
			if strings.Contains(q, "*") || strings.Contains(q, "?") {
				processed = regexp.QuoteMeta(q)
				processed = strings.ReplaceAll(processed, "\\*", ".*")
				processed = strings.ReplaceAll(processed, "\\?", ".")
				if !strings.HasPrefix(q, "*") {
					processed = "^" + processed
				}
				if !strings.HasSuffix(q, "*") {
					processed = processed + "$"
				}
			}

			// Compile Regex (Case-Insensitive by default)
			re, err := regexp.Compile("(?i)" + processed)
			if err != nil {
				// If it's not a valid regex, treat it as a literal string (escaped)
				re, _ = regexp.Compile("(?i)" + regexp.QuoteMeta(processed))
			}
			regexes = append(regexes, re)
		}

		if len(regexes) == 0 {
			return "No valid search query provided.", nil
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Searching for '%s' in %s:\n", args.Query, walkPath))
		count := 0

		walkErr := filepath.Walk(walkPath, func(p string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}

			// Exclusions
			if info.IsDir() {
				name := info.Name()
				if name == ".git" || name == "node_modules" || name == "vendor" || name == "logs" || name == "scratch" || (strings.HasPrefix(name, ".") && name != ".") {
					return filepath.SkipDir
				}
				return nil
			}

			rel, _ := filepath.Rel(walkRoot, p)
			
			// Match any of the regexes
			pathMatched := false
			for _, re := range regexes {
				if re.MatchString(rel) {
					pathMatched = true
					break
				}
			}

			// 2. Check content line by line
			file, err := os.Open(p)
			if err != nil {
				return nil // Skip unreadable
			}
			defer file.Close()

			// Skip binary files (heuristic: check first 512 bytes)
			head := make([]byte, 512)
			n, _ := file.Read(head)
			for i := 0; i < n; i++ {
				if head[i] == 0 {
					return nil // Likely binary
				}
			}
			file.Seek(0, 0)

			scanner := bufio.NewScanner(file)
			buf := make([]byte, 0, 64*1024)
			scanner.Buffer(buf, 1024*1024)

			lineNum := 1
			var snippets []string
			contentMatched := false

			for scanner.Scan() {
				line := scanner.Text()
				for _, re := range regexes {
					if re.MatchString(line) {
						contentMatched = true
						if len(snippets) < 3 {
							trimmed := strings.TrimSpace(line)
							if len(trimmed) > 120 {
								trimmed = trimmed[:120] + "..."
							}
							snippets = append(snippets, fmt.Sprintf("  L%d: %s", lineNum, trimmed))
						}
						break
					}
				}
				lineNum++
				if lineNum > 10000 && !contentMatched && !pathMatched {
					break
				}
			}

			if pathMatched || contentMatched {
				sb.WriteString(fmt.Sprintf("- %s\n", rel))
				for _, s := range snippets {
					sb.WriteString(s + "\n")
				}
				count++
			}

			if count >= 50 {
				return fmt.Errorf("LIMIT_REACHED")
			}
			return nil
		})

		if walkErr != nil && walkErr.Error() != "LIMIT_REACHED" {
			return "", fmt.Errorf("search failed: %w", walkErr)
		}
		if count == 0 {
			return "No matches found.", nil
		}
		if walkErr != nil && walkErr.Error() == "LIMIT_REACHED" {
			sb.WriteString("\n... (truncated: too many results)")
		}
		return sb.String(), nil

	case "outline":
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}

		fset := token.NewFileSet()
		var sb strings.Builder

		if info.IsDir() {
			pkgs, err := parser.ParseDir(fset, path, nil, parser.ParseComments)
			if err != nil {
				return "", fmt.Errorf("failed to parse directory %s: %w", path, err)
			}
			sb.WriteString(fmt.Sprintf("Outline of package in directory %s:\n", args.Path))
			for pkgName, pkg := range pkgs {
				sb.WriteString(fmt.Sprintf("Package: %s\n", pkgName))
				for filename, file := range pkg.Files {
					sb.WriteString(fmt.Sprintf("  File: %s\n", filepath.Base(filename)))
					writeASTFileOutline(&sb, file, fset)
				}
			}
		} else {
			if !strings.HasSuffix(strings.ToLower(path), ".go") {
				return "", fmt.Errorf("outline action is only supported for Go (.go) source files")
			}
			file, err := parser.ParseFile(fset, path, nil, parser.ParseComments)
			if err != nil {
				return "", fmt.Errorf("failed to parse file %s: %w", path, err)
			}
			sb.WriteString(fmt.Sprintf("Outline of file %s:\n", args.Path))
			writeASTFileOutline(&sb, file, fset)
		}
		return sb.String(), nil

	case "dependencies":
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}

		var goFiles []string
		if info.IsDir() {
			entries, err := os.ReadDir(path)
			if err != nil {
				return "", err
			}
			for _, entry := range entries {
				if !entry.IsDir() && strings.HasSuffix(strings.ToLower(entry.Name()), ".go") {
					goFiles = append(goFiles, filepath.Join(path, entry.Name()))
				}
			}
		} else {
			if !strings.HasSuffix(strings.ToLower(path), ".go") {
				return "", fmt.Errorf("dependencies action is only supported for Go (.go) files or directories containing them")
			}
			goFiles = append(goFiles, path)
		}

		fset := token.NewFileSet()
		importMap := make(map[string]bool)

		for _, goFile := range goFiles {
			f, err := parser.ParseFile(fset, goFile, nil, parser.ImportsOnly)
			if err == nil {
				for _, imp := range f.Imports {
					pathVal := strings.Trim(imp.Path.Value, "\"")
					importMap[pathVal] = true
				}
			}
		}

		var stdLib, localMod, thirdParty []string
		for imp := range importMap {
			if isStdLib(imp) {
				stdLib = append(stdLib, imp)
			} else if strings.Contains(imp, "github.com/user/cognitive-orchestrator") || strings.Contains(imp, "github.com/ArtifactFlow/ArtifactFlow") {
				localMod = append(localMod, imp)
			} else {
				thirdParty = append(thirdParty, imp)
			}
		}

		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Dependency imports for %s:\n", args.Path))
		sb.WriteString("\n[STANDARD LIBRARY]:\n")
		if len(stdLib) == 0 {
			sb.WriteString("  (none)\n")
		}
		for _, imp := range stdLib {
			sb.WriteString(fmt.Sprintf("  - %s\n", imp))
		}

		sb.WriteString("\n[LOCAL MODULES]:\n")
		if len(localMod) == 0 {
			sb.WriteString("  (none)\n")
		}
		for _, imp := range localMod {
			sb.WriteString(fmt.Sprintf("  - %s\n", imp))
		}

		sb.WriteString("\n[THIRD PARTY]:\n")
		if len(thirdParty) == 0 {
			sb.WriteString("  (none)\n")
		}
		for _, imp := range thirdParty {
			sb.WriteString(fmt.Sprintf("  - %s\n", imp))
		}

		return sb.String(), nil

	default:
		return "", fmt.Errorf("unknown action: %s", action)
	}
}

func writeASTFileOutline(sb *strings.Builder, file *ast.File, fset *token.FileSet) {
	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.GenDecl:
			if d.Tok.String() == "type" {
				for _, spec := range d.Specs {
					if typeSpec, ok := spec.(*ast.TypeSpec); ok {
						switch t := typeSpec.Type.(type) {
						case *ast.StructType:
							sb.WriteString(fmt.Sprintf("    type %s struct {\n", typeSpec.Name.Name))
							if t.Fields != nil {
								for _, field := range t.Fields.List {
									fieldTypeStr := formatType(field.Type)
									if len(field.Names) > 0 {
										for _, name := range field.Names {
											sb.WriteString(fmt.Sprintf("        %s %s\n", name.Name, fieldTypeStr))
										}
									} else {
										sb.WriteString(fmt.Sprintf("        %s\n", fieldTypeStr))
									}
								}
							}
							sb.WriteString("    }\n")
						case *ast.InterfaceType:
							sb.WriteString(fmt.Sprintf("    type %s interface {\n", typeSpec.Name.Name))
							if t.Methods != nil {
								for _, method := range t.Methods.List {
									methodTypeStr := formatType(method.Type)
									if len(method.Names) > 0 {
										sb.WriteString(fmt.Sprintf("        %s%s\n", method.Names[0].Name, strings.TrimPrefix(methodTypeStr, "func")))
									} else {
										sb.WriteString(fmt.Sprintf("        %s\n", methodTypeStr))
									}
								}
							}
							sb.WriteString("    }\n")
						default:
							sb.WriteString(fmt.Sprintf("    type %s %s\n", typeSpec.Name.Name, formatType(typeSpec.Type)))
						}
					}
				}
			}
		case *ast.FuncDecl:
			recvStr := ""
			if d.Recv != nil && len(d.Recv.List) > 0 {
				recvField := d.Recv.List[0]
				recvType := formatType(recvField.Type)
				recvName := ""
				if len(recvField.Names) > 0 {
					recvName = recvField.Names[0].Name + " "
				}
				recvStr = fmt.Sprintf("(%s%s) ", recvName, recvType)
			}
			
			var params []string
			if d.Type.Params != nil {
				for _, p := range d.Type.Params.List {
					pType := formatType(p.Type)
					if len(p.Names) > 0 {
						for _, name := range p.Names {
							params = append(params, fmt.Sprintf("%s %s", name.Name, pType))
						}
					} else {
						params = append(params, pType)
					}
				}
			}
			
			var returns []string
			if d.Type.Results != nil {
				for _, r := range d.Type.Results.List {
					rType := formatType(r.Type)
					if len(r.Names) > 0 {
						for _, name := range r.Names {
							returns = append(returns, fmt.Sprintf("%s %s", name.Name, rType))
						}
					} else {
						returns = append(returns, rType)
					}
				}
			}
			
			retStr := ""
			if len(returns) == 1 {
				retStr = " " + returns[0]
			} else if len(returns) > 1 {
				retStr = " (" + strings.Join(returns, ", ") + ")"
			}
			
			sb.WriteString(fmt.Sprintf("    func %s%s(%s)%s\n", recvStr, d.Name.Name, strings.Join(params, ", "), retStr))
		}
	}
}

func formatType(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + formatType(t.X)
	case *ast.SelectorExpr:
		return formatType(t.X) + "." + t.Sel.Name
	case *ast.ArrayType:
		lenStr := ""
		if t.Len != nil {
			lenStr = "..."
		}
		return "[" + lenStr + "]" + formatType(t.Elt)
	case *ast.MapType:
		return "map[" + formatType(t.Key) + "]" + formatType(t.Value)
	case *ast.InterfaceType:
		return "interface{}"
	case *ast.FuncType:
		var params []string
		if t.Params != nil {
			for _, p := range t.Params.List {
				params = append(params, formatType(p.Type))
			}
		}
		var results []string
		if t.Results != nil {
			for _, r := range t.Results.List {
				results = append(results, formatType(r.Type))
			}
		}
		ret := ""
		if len(results) == 1 {
			ret = " " + results[0]
		} else if len(results) > 1 {
			ret = " (" + strings.Join(results, ", ") + ")"
		}
		return "func(" + strings.Join(params, ", ") + ")" + ret
	case *ast.ChanType:
		dir := "chan "
		if t.Dir == ast.SEND {
			dir = "chan<- "
		} else if t.Dir == ast.RECV {
			dir = "<-chan "
		}
		return dir + formatType(t.Value)
	case *ast.Ellipsis:
		return "..." + formatType(t.Elt)
	case *ast.StructType:
		return "struct{}"
	default:
		return fmt.Sprintf("%T", expr)
	}
}

func isStdLib(imp string) bool {
	firstPart := strings.Split(imp, "/")[0]
	return !strings.Contains(firstPart, ".")
}
