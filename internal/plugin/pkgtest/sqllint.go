package pkgtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/database"
)

// The queries a backend runs go to whichever engine the instance uses, and
// nothing compiles them until they run — a statement that SQLite accepts and
// PostgreSQL does not is found by an operator on the engine the plugin was not
// tried on. The migrations are held to a list of engine-specific spellings
// (database.PortabilityProblems); this is the same idea for the statements in
// the backend's Go source, with the spellings that only turn up in queries.
var runtimeNonPortable = []struct {
	pattern *regexp.Regexp
	why     string
}{
	{regexp.MustCompile(`(?i)\bINSERT\s+OR\s+(IGNORE|REPLACE)\b`), "SQLite only; use ON CONFLICT"},
	{regexp.MustCompile(`(?i)\bREPLACE\s+INTO\b`), "SQLite only; use ON CONFLICT"},
	{regexp.MustCompile(`(?i)\b(datetime|strftime|julianday|unixepoch)\s*\(`), "SQLite only; timestamps here are epoch milliseconds from Go"},
	{regexp.MustCompile(`(?i)\bifnull\s*\(`), "SQLite only; use COALESCE"},
	{regexp.MustCompile(`(?i)\bglob\b`), "SQLite only"},
	{regexp.MustCompile(`(?i)\b(randomblob|last_insert_rowid|typeof|printf)\s*\(`), "SQLite only"},
	{regexp.MustCompile(`(?i)\brandom\s*\(\s*\)`), "differs between the engines; ids come from the host"},
	{regexp.MustCompile(`(?i)\browid\b`), "SQLite only"},
	{regexp.MustCompile(`(?i)\binstr\s*\(`), "SQLite only; PostgreSQL has no instr"},
	{regexp.MustCompile(`(?i)\bgroup_concat\s*\(`), "SQLite only; PostgreSQL calls it string_agg"},
	{regexp.MustCompile(`(?i)\bjson_(extract|each|tree)\s*\(`), "SQLite only; JSON is stored as TEXT"},
	{regexp.MustCompile(`(?i)\bLIMIT\s+-\s*1\b`), "SQLite only"},
	{regexp.MustCompile(`(?i)\bPRAGMA\b`), "SQLite only"},
	{regexp.MustCompile(`\w::\w`), "PostgreSQL only; use CAST(... AS ...)"},
}

var looksLikeSQL = regexp.MustCompile(`(?is)^\s*(SELECT|INSERT|UPDATE|DELETE|WITH|REPLACE)\b`)

// SQLProblems reads the Go sources directly in dir (tests excluded) and
// returns a line for each statement in them that uses a spelling only one of
// the two engines has. A string constant is taken to be a statement when it
// starts like one; constants joined with + are read as the one string they
// make. It is a lint, not a parser: it cannot know what a query does, but the
// mistakes it looks for are the ones that cost a deployment.
func SQLProblems(dir string) ([]string, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.go"))
	if err != nil {
		return nil, err
	}
	var out []string
	fset := token.NewFileSet()
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		src, err := os.ReadFile(name)
		if err != nil {
			return nil, err
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			return nil, err
		}
		ast.Inspect(file, func(n ast.Node) bool {
			expr, ok := n.(ast.Expr)
			if !ok {
				return true
			}
			sql, ok := constantString(expr)
			if !ok {
				return true
			}
			if !looksLikeSQL.MatchString(stripLineComments(sql)) {
				// A literal that is one side of a larger concatenation is
				// visited on its own too, so nothing is lost by moving on.
				return true
			}
			position := fset.Position(expr.Pos())
			for _, problem := range problemsIn(sql) {
				out = append(out, fmt.Sprintf("%s:%d: %s", filepath.Base(position.Filename), position.Line, problem))
			}
			// The pieces of a concatenation are inside this expression; they
			// have been read as part of it.
			return false
		})
	}
	sort.Strings(out)
	return out, nil
}

func problemsIn(sql string) []string {
	body := stripLineComments(sql)
	problems := database.PortabilityProblems(sql)
	for _, rule := range runtimeNonPortable {
		if match := rule.pattern.FindString(body); match != "" {
			problems = append(problems, "\""+strings.TrimSpace(match)+"\" is not portable — "+rule.why)
		}
	}
	return problems
}

func stripLineComments(sql string) string {
	lines := strings.Split(sql, "\n")
	for i, line := range lines {
		if index := strings.Index(line, "--"); index >= 0 {
			lines[i] = line[:index]
		}
	}
	return strings.Join(lines, "\n")
}

// constantString is the value of a string literal, or of literals joined by +.
func constantString(expr ast.Expr) (string, bool) {
	switch e := expr.(type) {
	case *ast.BasicLit:
		if e.Kind != token.STRING {
			return "", false
		}
		value, err := strconv.Unquote(e.Value)
		return value, err == nil
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		left, ok := constantString(e.X)
		if !ok {
			return "", false
		}
		right, ok := constantString(e.Y)
		if !ok {
			return "", false
		}
		return left + right, true
	case *ast.ParenExpr:
		return constantString(e.X)
	}
	return "", false
}
