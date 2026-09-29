package hookd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestPostgresRepositoryTx_HasNoQueriesOfItsOwn pins go-hookd#8's fix: the tx
// type's data operations are the embedded pgStore's (the SAME code the pool
// repository runs), so it may only define transaction control and the
// connection-level methods. A re-added query method would be a second copy
// free to drift from the schema again.
func TestPostgresRepositoryTx_HasNoQueriesOfItsOwn(t *testing.T) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, ".", func(fi fs.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	require.NoError(t, err)

	var own []string
	for _, pkg := range pkgs {
		for _, f := range pkg.Files {
			for _, decl := range f.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Recv == nil || len(fn.Recv.List) != 1 {
					continue
				}
				star, ok := fn.Recv.List[0].Type.(*ast.StarExpr)
				if !ok {
					continue
				}
				if id, ok := star.X.(*ast.Ident); ok && id.Name == "PostgresRepositoryTx" {
					own = append(own, fn.Name.Name)
				}
			}
		}
	}
	sort.Strings(own)
	assert.Equal(t, []string{"BeginTx", "Close", "Commit", "Ping", "Rollback"}, own)

	// And both repositories really do get their data methods from pgStore.
	var _ RepositoryTx = (*PostgresRepositoryTx)(nil)
	var _ Repository = (*PostgresRepository)(nil)
}
