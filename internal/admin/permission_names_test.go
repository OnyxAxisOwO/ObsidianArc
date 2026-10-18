package admin

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/OnyxAxisOwO/ObsidianArc/internal/settings"
	"github.com/OnyxAxisOwO/ObsidianArc/internal/user"
)

// knownGrant is a name hasPermission can say yes to. Anything else is refused
// to every account, the super administrator's included, because CanAdmin asks
// ValidPermission before it asks the role.
func knownGrant(name string) bool {
	return name == "super_admin" || user.ValidPermission(name)
}

// A grant on a route is a string, split at run time. A misspelt one compiles,
// passes every test that does not open that page, and shuts the page to
// everybody, so each one written in this package's sources is read here.
func TestEveryRouteGrantIsOneThatExists(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatal(err)
	}
	fset := token.NewFileSet()
	checked := 0
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", file, err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok || len(call.Args) == 0 {
				return true
			}
			var name string
			switch fn := call.Fun.(type) {
			case *ast.Ident:
				name = fn.Name
			case *ast.SelectorExpr:
				name = fn.Sel.Name
			}
			if name != "protected" && name != "Protect" {
				return true
			}
			literal, ok := call.Args[0].(*ast.BasicLit)
			if !ok || literal.Kind != token.STRING {
				return true
			}
			grants, err := strconv.Unquote(literal.Value)
			if err != nil {
				t.Fatalf("%s: %v", fset.Position(literal.Pos()), err)
			}
			// No grant at all is a route any administrator may use.
			if grants == "" {
				checked++
				return true
			}
			for _, grant := range strings.Split(grants, ",") {
				checked++
				if !knownGrant(grant) {
					t.Errorf("%s: route grant %q is not one an administrator can hold", fset.Position(literal.Pos()), grant)
				}
			}
			return true
		})
	}
	if checked == 0 {
		t.Fatal("found no route grants to check; the test no longer reads the route table")
	}
}

// The grants a core setting is filed under, by the prefix rule.
func TestEveryCoreSettingIsFiledUnderAGrantThatExists(t *testing.T) {
	for key := range settings.Defaults {
		for _, grant := range strings.Split(corePermission(key), ",") {
			if !knownGrant(grant) {
				t.Errorf("setting %q is filed under %q, which no administrator can hold", key, grant)
			}
		}
	}
}

// Which provider app a sign-in is made through, and every address it reaches,
// decide whose word an account is opened on. The prefix rule puts oauth. under
// the delegable security grant, so a new credential or address that is not
// also listed in superAdminSettings would quietly become delegable.
func TestEveryOAuthIdentityOrAddressIsTheSuperAdministrators(t *testing.T) {
	for key := range settings.Defaults {
		if !strings.HasPrefix(key, "oauth.") {
			continue
		}
		if !strings.Contains(key, "client_") && !strings.HasSuffix(key, "_url") && !strings.HasSuffix(key, "_issuer") {
			continue
		}
		if !superAdminSettings[key] {
			t.Errorf("%q names a provider's app or an address the sign-in reaches, and is not in superAdminSettings", key)
		}
	}
}
