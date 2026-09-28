package hookd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Source pins (modelled on thalamus's webhook egress pins): they fail if a
// future change adds a second, quieter way to loosen the guard or to sign with
// an unresolved secret.

// productionFiles parses every non-test .go file of the root package.
func productionFiles(t *testing.T) (*token.FileSet, map[string]*ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	paths, err := filepath.Glob("*.go")
	require.NoError(t, err)
	files := map[string]*ast.File{}
	for _, p := range paths {
		if strings.HasSuffix(p, "_test.go") {
			continue
		}
		src, err := os.ReadFile(p)
		require.NoError(t, err)
		f, err := parser.ParseFile(fset, p, src, 0)
		require.NoError(t, err)
		files[p] = f
	}
	return fset, files
}

// assignmentsTo returns "file:func" for every assignment whose left side is a
// selector ending in field.
func assignmentsTo(files map[string]*ast.File, field string) []string {
	var sites []string
	for name, f := range files {
		for _, decl := range f.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			ast.Inspect(fn.Body, func(n ast.Node) bool {
				as, ok := n.(*ast.AssignStmt)
				if !ok {
					return true
				}
				for _, lhs := range as.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == field {
						sites = append(sites, name+":"+fn.Name.Name)
					}
				}
				return true
			})
		}
	}
	return sites
}

func TestEgressPin_OnlyTheOptInLoosensThePolicy(t *testing.T) {
	_, files := productionFiles(t)
	assert.ElementsMatch(t, []string{"hookd.egress.go:WithAllowPrivateDestinations"}, assignmentsTo(files, "allowPrivate"),
		"egressPolicy.allowPrivate may be set ONLY by WithAllowPrivateDestinations")
	assert.ElementsMatch(t, []string{}, assignmentsTo(files, "egressPolicy"),
		"the Manager's policy must never be replaced wholesale")
}

func TestEgressPin_DeliveryClientOnlyFromBuildHTTPClient(t *testing.T) {
	_, files := productionFiles(t)
	for _, site := range assignmentsTo(files, "httpClient") {
		assert.Equal(t, "hookd.egress.go:buildHTTPClient", site, "m.httpClient assigned outside buildHTTPClient")
	}
}

// TestSecretPin_SignaturesUseResolvedSecrets: calculateSignature is never handed
// a stored field (sub.Secret, delivery.Secret) directly — only a value that went
// through resolveSigningSecret.
func TestSecretPin_SignaturesUseResolvedSecrets(t *testing.T) {
	_, files := productionFiles(t)
	calls := 0
	for name, f := range files {
		ast.Inspect(f, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			id, ok := call.Fun.(*ast.Ident)
			if !ok || id.Name != "calculateSignature" || len(call.Args) == 0 {
				return true
			}
			calls++
			_, isSelector := call.Args[0].(*ast.SelectorExpr)
			assert.False(t, isSelector, "%s: calculateSignature signs a stored field directly", name)
			return true
		})
	}
	// delivery, TestSubscription, and the receiver-side VerifySignature helper
	assert.Equal(t, 3, calls, "a new signing site must be reviewed against the resolver contract")
}
