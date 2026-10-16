// Command ledger builds a file/function inventory ledger for a code review.
// It is read-only: it walks the repository (git ls-files), parses Go with go/ast,
// and extracts TypeScript/TSX and Python declarations with regular expressions,
// then writes CSV ledgers to the requested output directory.
//
// Revision 2 (2026-10-16): the first revision classified .tsx as lang "tsx" but the
// dispatch switch only matched "ts", so every TSX file fell through to the SQL
// extractor and contributed zero functions. This revision handles "tsx" explicitly.
package main

import (
	"encoding/csv"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

type fileRow struct {
	Path       string
	Lang       string
	Kind       string
	Module     string
	Lines      int
	FuncCount  int
	ReviewStat string
	Note       string
}

type funcRow struct {
	Path  string
	Name  string
	Kind  string
	Start int
	End   int
	Lang  string
}

var ownPrefixes = []string{
	"internal/", "cmd/", "web/src/", "web/tests/", "web/test/", "services/", "test/",
	"scripts/", "migrations/", "deploy/", "api/", "policies/", "recipes/", "gen/",
	"build/", "artifacts/", "bundle/", "third_party/", "poc/",
}

var skipDirs = map[string]bool{
	"node_modules": true, "dist": true, ".git": true, ".worktrees": true,
}

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintln(os.Stderr, "usage: ledger <repo-root> <out-dir>")
		os.Exit(2)
	}
	root := os.Args[1]
	out := os.Args[2]
	files := listTracked(root)
	sort.Strings(files)

	var fileRows []fileRow
	var funcRows []funcRow
	for _, rel := range files {
		if !owned(rel) {
			continue
		}
		abs := filepath.Join(root, rel)
		info, err := os.Stat(abs)
		if err != nil || info.IsDir() {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		contents := string(data)
		lines := strings.Count(contents, "\n")
		if len(data) > 0 && !strings.HasSuffix(contents, "\n") {
			lines++
		}
		lang, kind := classify(rel)
		var fs []funcRow
		switch lang {
		case "go":
			fs = goFunctions(rel, abs)
		case "ts", "tsx":
			fs = tsFunctions(rel, contents, lang)
		case "python":
			fs = pyFunctions(rel, contents)
		default:
			fs = sqlFunctions(rel, contents)
		}
		funcRows = append(funcRows, fs...)
		fileRows = append(fileRows, fileRow{
			Path: rel, Lang: lang, Kind: kind, Module: moduleOf(rel), Lines: lines,
			FuncCount: len(fs), ReviewStat: reviewStatus(rel, kind), Note: note(rel),
		})
	}

	writeCSV(filepath.Join(out, "coverage.csv"),
		[]string{"path", "lang", "kind", "module", "lines", "function_count", "manual_review_status", "note"},
		func() [][]string {
			var rows [][]string
			for _, r := range fileRows {
				rows = append(rows, []string{r.Path, r.Lang, r.Kind, r.Module,
					strconv.Itoa(r.Lines), strconv.Itoa(r.FuncCount), r.ReviewStat, r.Note})
			}
			return rows
		}())

	writeCSV(filepath.Join(out, "functions.csv"),
		[]string{"path", "name", "kind", "start_line", "end_line", "lang"},
		func() [][]string {
			var rows [][]string
			for _, r := range funcRows {
				rows = append(rows, []string{r.Path, r.Name, r.Kind,
					strconv.Itoa(r.Start), strconv.Itoa(r.End), r.Lang})
			}
			return rows
		}())

	fmt.Printf("files=%d functions=%d\n", len(fileRows), len(funcRows))
}

func listTracked(root string) []string {
	cmd := exec.Command("git", "ls-files")
	cmd.Dir = root
	raw, err := cmd.Output()
	if err != nil {
		fmt.Fprintln(os.Stderr, "git ls-files failed:", err)
		os.Exit(1)
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		line = strings.Trim(line, "\"")
		if line == "" || strings.Contains(line, `\/`) {
			continue
		}
		skip := false
		for part := range strings.SplitSeq(line, "/") {
			if skipDirs[part] {
				skip = true
				break
			}
		}
		if !skip {
			out = append(out, line)
		}
	}
	return out
}

func owned(rel string) bool {
	for _, p := range ownPrefixes {
		if strings.HasPrefix(rel, p) {
			return true
		}
	}
	return false
}

func moduleOf(rel string) string {
	parts := strings.Split(rel, "/")
	if strings.HasPrefix(rel, "web/") && len(parts) > 1 {
		return strings.Join(parts[:min(2, len(parts))], "/")
	}
	if len(parts) > 1 {
		return parts[0] + "/" + parts[1]
	}
	return parts[0]
}

func classify(rel string) (string, string) {
	ext := strings.ToLower(filepath.Ext(rel))
	base := filepath.Base(rel)
	test := strings.Contains(rel, "_test.") || strings.HasSuffix(base, ".test.ts") || strings.HasSuffix(base, ".test.tsx") ||
		strings.HasSuffix(base, ".spec.ts") || strings.Contains(rel, "/tests/")
	generated := strings.Contains(rel, "/generated/") || strings.HasSuffix(base, ".gen.go") ||
		strings.Contains(rel, "/dbgen/") || strings.HasSuffix(base, "-resolved.yaml") ||
		strings.HasSuffix(base, ".lock.json") || strings.HasSuffix(base, ".tgz")
	kind := "source"
	if test {
		kind = "test"
	} else if generated {
		kind = "generated"
	} else if strings.HasPrefix(rel, "deploy/") || strings.HasPrefix(rel, "migrations/") ||
		strings.HasPrefix(rel, "policies/") || strings.HasPrefix(rel, "recipes/") ||
		strings.HasPrefix(rel, "api/") || strings.HasPrefix(rel, "build/") {
		kind = "config"
	} else if strings.HasPrefix(rel, "third_party/") || strings.HasPrefix(rel, "bundle/") {
		kind = "third_party"
	} else if ext == ".md" {
		kind = "doc"
	}
	switch ext {
	case ".go":
		return "go", kind
	case ".ts":
		return "ts", kind
	case ".tsx":
		return "tsx", kind
	case ".py":
		return "python", kind
	case ".sql":
		return "sql", kind
	case ".json", ".jsonl", ".yaml", ".yml", ".toml", ".rego", ".csv", ".fixture":
		return "config", kind
	case ".md":
		return "doc", kind
	case ".sh", ".mjs":
		return "script", kind
	default:
		return "other", kind
	}
}

func goFunctions(rel, abs string) []funcRow {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, abs, nil, parser.SkipObjectResolution)
	if err != nil {
		return nil
	}
	var rows []funcRow
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		name := fn.Name.Name
		if fn.Recv != nil && len(fn.Recv.List) > 0 {
			name = recvName(fn.Recv.List[0].Type) + "." + name
		}
		rows = append(rows, funcRow{
			Path: rel, Name: name, Kind: "func",
			Start: fset.Position(fn.Pos()).Line,
			End:   fset.Position(fn.End()).Line, Lang: "go",
		})
	}
	return rows
}

func recvName(expr ast.Expr) string {
	switch t := expr.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return recvName(t.X)
	case *ast.IndexExpr:
		return recvName(t.X)
	default:
		return "?"
	}
}

// TypeScript/TSX declarations: function declarations, arrow/function consts, and
// typed consts (React components with an explicit type annotation).
var tsFuncRe = regexp.MustCompile(`(?m)^\s*(?:export\s+)?(?:default\s+)?(?:async\s+)?function\s+([A-Za-z0-9_$]+)|^\s*(?:export\s+)?(?:const|let)\s+([A-Za-z0-9_$]+)\s*[:=]\s*(?:async\s*)?\(|^\s*(?:export\s+)?(?:const|let)\s+([A-Za-z0-9_$]+)\s*=\s*(?:async\s*)?(?:function|\()`)

var pyFuncRe = regexp.MustCompile(`(?m)^(\s*)(?:async\s+)?def\s+([A-Za-z0-9_]+)\s*\(|^(\s*)class\s+([A-Za-z0-9_]+)\s*[:(]`)

var sqlFuncRe = regexp.MustCompile(`(?mi)^\s*CREATE\s+(?:OR\s+REPLACE\s+)?FUNCTION\s+([a-zA-Z0-9_.]+)`)

func tsFunctions(rel, contents, lang string) []funcRow {
	return regexFunctions(rel, contents, lang, tsFuncRe)
}

func pyFunctions(rel, contents string) []funcRow {
	return regexFunctions(rel, contents, "python", pyFuncRe)
}

func regexFunctions(rel, contents, lang string, re *regexp.Regexp) []funcRow {
	lines := strings.Split(contents, "\n")
	var rows []funcRow
	for i, line := range lines {
		m := re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := ""
		for _, cand := range m[1:] {
			if cand != "" {
				name = cand
				break
			}
		}
		if name == "" {
			continue
		}
		rows = append(rows, funcRow{Path: rel, Name: name, Kind: "func", Start: i + 1, End: 0, Lang: lang})
	}
	// approximate: a declaration spans until the line before the next declaration
	for i := range rows {
		if i+1 < len(rows) {
			rows[i].End = rows[i+1].Start - 1
		} else {
			rows[i].End = len(lines)
		}
	}
	return rows
}

func sqlFunctions(rel, contents string) []funcRow {
	var rows []funcRow
	for _, m := range sqlFuncRe.FindAllStringSubmatchIndex(contents, -1) {
		name := contents[m[2]:m[3]]
		start := strings.Count(contents[:m[0]], "\n") + 1
		rows = append(rows, funcRow{Path: rel, Name: name, Kind: "sql_function", Start: start, End: 0, Lang: "sql"})
	}
	return rows
}

func reviewStatus(rel, kind string) string {
	switch {
	case kind == "generated":
		return "生成物(不逐行)"
	case kind == "third_party":
		return "第三方(来源/准入核对)"
	case strings.HasPrefix(rel, "docs/"):
		return "文档"
	case strings.HasPrefix(rel, "web/src/api/generated/"):
		return "生成物(不逐行)"
	case strings.HasPrefix(rel, "internal/investigation/"), strings.HasPrefix(rel, "internal/analytics/"),
		strings.HasPrefix(rel, "internal/assistant/"), strings.HasPrefix(rel, "internal/resourcestore/"),
		strings.HasPrefix(rel, "internal/resource/"), strings.HasPrefix(rel, "internal/graph/"),
		strings.HasPrefix(rel, "internal/incident/"), strings.HasPrefix(rel, "internal/finding/"),
		strings.HasPrefix(rel, "internal/rca/"), strings.HasPrefix(rel, "internal/action/"),
		strings.HasPrefix(rel, "internal/evidence/"), strings.HasPrefix(rel, "internal/archive/"),
		strings.HasPrefix(rel, "internal/inspection/"):
		return "非测试已逐行(测试部分)"
	case strings.HasPrefix(rel, "internal/httpapi/"), strings.HasPrefix(rel, "internal/app/"),
		strings.HasPrefix(rel, "internal/auth/"), strings.HasPrefix(rel, "internal/tenant/"),
		strings.HasPrefix(rel, "internal/source/"), strings.HasPrefix(rel, "internal/audit/"),
		strings.HasPrefix(rel, "internal/policy/"), strings.HasPrefix(rel, "internal/upstream/"),
		strings.HasPrefix(rel, "internal/supplychain/"), strings.HasPrefix(rel, "internal/bundle/"),
		strings.HasPrefix(rel, "internal/integrations/"), strings.HasPrefix(rel, "internal/persistence/"),
		strings.HasPrefix(rel, "internal/observability/"), strings.HasPrefix(rel, "internal/profile/"),
		strings.HasPrefix(rel, "internal/configregistry/"), strings.HasPrefix(rel, "internal/bootstrap/"),
		strings.HasPrefix(rel, "internal/modelprofile/"), strings.HasPrefix(rel, "internal/crypto/"),
		strings.HasPrefix(rel, "internal/contract/"), strings.HasPrefix(rel, "internal/datascope/"),
		strings.HasPrefix(rel, "cmd/"):
		return "非测试大部分逐行/部分未读"
	case strings.HasPrefix(rel, "services/investigator/src/"):
		return "已逐行(生产)"
	case strings.HasPrefix(rel, "services/investigator/tests/"):
		return "已逐行(测试)"
	case strings.HasPrefix(rel, "web/src/"), strings.HasPrefix(rel, "web/tests/"):
		return "已逐行(静态)"
	case strings.HasPrefix(rel, "test/"):
		return "抽样/门禁分类"
	case strings.HasPrefix(rel, "migrations/"), strings.HasPrefix(rel, "deploy/"):
		return "已逐行(配置)"
	case strings.HasPrefix(rel, "scripts/"):
		return "部分逐行"
	default:
		return "盘点级"
	}
}

func note(rel string) string {
	switch {
	case strings.HasPrefix(rel, "deploy/addons/kubevirt/"):
		return "ADR-0008 延期物料(只读核对)"
	case strings.Contains(rel, "third_party/"):
		return "第三方来源/许可/锁文件"
	case strings.HasSuffix(rel, ".tsx") || strings.HasSuffix(rel, ".ts"):
		return "TS/TSX 函数跨度为正则近似"
	case strings.HasSuffix(rel, ".py"):
		return "Python 函数跨度为正则近似"
	default:
		return ""
	}
}

func writeCSV(path string, header []string, rows [][]string) {
	f, err := os.Create(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, "create:", err)
		os.Exit(1)
	}
	defer f.Close()
	w := csv.NewWriter(f)
	_ = w.Write(header)
	for _, r := range rows {
		_ = w.Write(r)
	}
	w.Flush()
}
