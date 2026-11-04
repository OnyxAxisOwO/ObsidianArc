package pkgtest

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
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

// PRAGMA and DDL are read as SQL too: a backend that runs them is the case the
// lint exists for, and the object keyword after CREATE, ALTER and DROP keeps
// prose such as "create a user" from being taken for a statement.
var looksLikeSQL = regexp.MustCompile(`(?is)^\s*(SELECT|INSERT|UPDATE|DELETE|WITH|REPLACE|PRAGMA|CREATE\s+(TABLE|INDEX|VIEW|TRIGGER)|ALTER\s+TABLE|DROP\s+(TABLE|INDEX|VIEW|TRIGGER))\b`)

// A piece of a statement, most often a condition or clause a backend adds to a
// query with +, is checked as a statement is, so a spelling that is not portable
// inside one is found too. Such a piece starts with a clause keyword: after
// leading whitespace in any case (" and x = ?"), or at the very start in capitals
// ("WHERE x = ?"). The capitals-only form is what keeps a sentence such as
// "Set the glob pattern" from being read as SQL.
var looksLikeFragment = regexp.MustCompile(`^(\s+(?i:` + clauseKeywords + `)|\s*(` + clauseKeywords + `))\b`)

const clauseKeywords = `AND|OR|ON|WHERE|FROM|JOIN|ORDER\s+BY|GROUP\s+BY|HAVING|LIMIT|OFFSET|RETURNING|ON\s+CONFLICT|SET|VALUES`

// SQLProblems reads the Go sources under dir (tests excluded, and directories
// the Go tool skips: hidden, underscore-prefixed and testdata) and returns a
// line for each statement in them that uses a spelling only one of the two
// engines has. A string constant is taken to be a statement when it starts like
// one, or like a clause; constants joined with + are read as the one string they
// make. It is a lint, not a parser: it cannot know what a query does, but the
// mistakes it looks for are the ones that cost a deployment.
func SQLProblems(dir string) ([]string, error) {
	var out []string
	fset := token.NewFileSet()
	err := filepath.WalkDir(dir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if name != dir && skippedByGoTool(entry.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		src, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, name, src, 0)
		if err != nil {
			return err
		}
		// Reported relative to dir, since a subdirectory's file can share a
		// base name with one at the top.
		rel, err := filepath.Rel(dir, name)
		if err != nil {
			return err
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
			body := database.StripSQLComments(sql)
			if !looksLikeSQL.MatchString(body) && !looksLikeFragment.MatchString(body) {
				// One side of a concatenation with a non-constant operand is
				// visited on its own, and is checked there if it reads as SQL.
				// A side that reads as neither (a bare name, say) is not checked.
				return true
			}
			position := fset.Position(expr.Pos())
			for _, problem := range problemsIn(sql) {
				out = append(out, fmt.Sprintf("%s:%d: %s", rel, position.Line, problem))
			}
			// The pieces of a concatenation are inside this expression; they
			// have been read as part of it.
			return false
		})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Strings(out)
	return out, nil
}

// skippedByGoTool is the directories the go command does not compile: hidden,
// underscore-prefixed, and testdata. Their sources are not part of the package,
// so a statement in one is not one the backend runs.
func skippedByGoTool(name string) bool {
	return strings.HasPrefix(name, ".") || strings.HasPrefix(name, "_") || name == "testdata"
}

func problemsIn(sql string) []string {
	body := database.StripSQLComments(sql)
	problems := database.PortabilityProblems(sql)
	for _, rule := range runtimeNonPortable {
		if match := rule.pattern.FindString(body); match != "" {
			problems = append(problems, "\""+strings.TrimSpace(match)+"\" is not portable — "+rule.why)
		}
	}
	return problems
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
