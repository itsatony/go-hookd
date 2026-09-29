package hookd

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// go-hookd#9: every identifier schema.sql derives from the prefix must fit
// PostgreSQL's 63-byte limit and stay distinct, for EVERY accepted prefix
// length — and for the prefixes in use today it must be byte-identical to the
// pre-v0.11.3 naming, so existing schemas never drift.

var (
	tmplObjectCall = regexp.MustCompile(`\{\{obj "(\w+)"\}\}`)
	tmplIdentCall  = regexp.MustCompile(`\{\{ident "(\w+)" "(\w+)"\}\}`)
)

// naiveExpansion renders schema.sql the pre-v0.11.3 way: plain concatenation,
// no shortening. It also returns every identifier it produced.
func naiveExpansion(prefix string) (string, []string) {
	var names []string
	s := tmplIdentCall.ReplaceAllStringFunc(schemaTemplate, func(m string) string {
		p := tmplIdentCall.FindStringSubmatch(m)
		n := fmt.Sprintf("%s_%s_hookd_%s", p[1], prefix, p[2])
		names = append(names, n)
		return n
	})
	s = tmplObjectCall.ReplaceAllStringFunc(s, func(m string) string {
		p := tmplObjectCall.FindStringSubmatch(m)
		n := fmt.Sprintf("%s_hookd_%s", prefix, p[1])
		names = append(names, n)
		return n
	})
	s = strings.ReplaceAll(s, "{{.Prefix}}", prefix)
	return s, names
}

func renderSchema(t *testing.T, prefix string) string {
	t.Helper()
	cfg, err := NewSchemaConfig(prefix)
	require.NoError(t, err)
	out, err := (&SchemaManager{schemaConfig: cfg}).processTemplate()
	require.NoError(t, err)
	return out
}

// longestNaiveIdentifier is the length of the longest name the naive
// expansion derives for a prefix of length n.
func longestNaiveIdentifier(n int) int {
	_, names := naiveExpansion(strings.Repeat("a", n))
	longest := 0
	for _, name := range names {
		if len(name) > longest {
			longest = len(name)
		}
	}
	return longest
}

func TestSchemaTemplate_SpellsEveryIdentifierThroughTheHelpers(t *testing.T) {
	// A raw "{{.Prefix}}_hookd_x" or "idx_{{.Prefix}}..." would bypass shortening.
	raw := regexp.MustCompile(`\{\{\.Prefix\}\}_hookd_\w|(idx|chk|fk|trg)_\{\{\.Prefix\}\}`)
	assert.Empty(t, raw.FindAllString(schemaTemplate, -1))
	assert.NotEmpty(t, tmplIdentCall.FindAllString(schemaTemplate, -1))
	assert.NotEmpty(t, tmplObjectCall.FindAllString(schemaTemplate, -1))
}

func TestSchemaIdentifiers_TodaysPrefixesAreUnchanged(t *testing.T) {
	// Every prefix whose longest naive name fits renders byte-identically to
	// the naive (pre-v0.11.3) expansion — including the fleet's own.
	fits := 0
	for n := 1; n <= MaxPrefixLength; n++ {
		if longestNaiveIdentifier(n) <= PostgresMaxIdentifierLength {
			fits = n
		}
	}
	require.GreaterOrEqual(t, fits, 20, "the unchanged zone must cover every short prefix")

	prefixes := []string{"ago", "agora", "trove", "tsr", "dpr", "test", "myservice", strings.Repeat("p", fits)}
	for _, p := range prefixes {
		naive, _ := naiveExpansion(p)
		assert.Equal(t, naive, renderSchema(t, p), "prefix %q must not drift", p)
	}
	// And the first length that does NOT fit is where shortening begins.
	if fits < MaxPrefixLength {
		p := strings.Repeat("p", fits+1)
		naive, _ := naiveExpansion(p)
		assert.NotEqual(t, naive, renderSchema(t, p))
	}
}

func TestSchemaIdentifiers_FitAndStayDistinctForEveryPrefixLength(t *testing.T) {
	for n := 1; n <= MaxPrefixLength; n++ {
		prefix := strings.Repeat("q", n)
		cfg, err := NewSchemaConfig(prefix)
		require.NoError(t, err)

		_, naive := naiveExpansion(prefix)
		rendered := map[string]string{} // rendered -> naive
		for _, full := range naive {
			var short string
			if m := regexp.MustCompile(`^(idx|chk|fk|trg)_` + prefix + `_hookd_(\w+)$`).FindStringSubmatch(full); m != nil {
				short = cfg.DerivedName(m[1], m[2])
			} else {
				short = ShortenIdentifier(full)
			}
			assert.LessOrEqual(t, len(short), PostgresMaxIdentifierLength, "prefix len %d: %s", n, full)
			if prev, ok := rendered[short]; ok {
				assert.Equal(t, prev, full, "prefix len %d: %q and %q collide as %q", n, prev, full, short)
			}
			rendered[short] = full
		}

		// Nothing in the rendered SQL may exceed the limit either.
		sql := renderSchema(t, prefix)
		for _, tok := range regexp.MustCompile(`\b\w*hookd\w*\b`).FindAllString(sql, -1) {
			assert.LessOrEqual(t, len(tok), PostgresMaxIdentifierLength, "prefix len %d: %s", n, tok)
		}

		// The public helpers agree with the template.
		for _, name := range append(cfg.AllTableNames(), cfg.AllFunctionNames()...) {
			assert.Contains(t, sql, name)
		}
		assert.Contains(t, sql, cfg.TriggerName("subscriptions", "updated_at"))
		assert.Contains(t, sql, cfg.TriggerName("circuit_breaker", "updated_at"))
		assert.Contains(t, sql, cfg.IndexName("subscriptions", "tenant_url"))
		assert.Contains(t, sql, cfg.CheckConstraintName("deliveries", "target"))
		assert.Contains(t, sql, cfg.ForeignKeyName("deliveries", "subscription"))
	}
}

func TestShortenIdentifier(t *testing.T) {
	fits := strings.Repeat("a", PostgresMaxIdentifierLength)
	assert.Equal(t, fits, ShortenIdentifier(fits), "a name that fits is never changed")
	assert.Equal(t, "idx_x", ShortenIdentifier("idx_x"))

	long1 := strings.Repeat("b", 70) + "_tenant_id"
	long2 := strings.Repeat("b", 70) + "_tenant_url"
	s1, s2 := ShortenIdentifier(long1), ShortenIdentifier(long2)
	assert.Len(t, s1, PostgresMaxIdentifierLength)
	assert.Len(t, s2, PostgresMaxIdentifierLength)
	assert.NotEqual(t, s1, s2, "names sharing their first 63 bytes stay distinct")
	assert.Equal(t, s1, ShortenIdentifier(long1), "deterministic")
	assert.Regexp(t, `^b+_[0-9a-f]{8}$`, s1)
}

func TestValidatePrefix_TooLongNamesTheLength(t *testing.T) {
	err := ValidatePrefix(strings.Repeat("a", MaxPrefixLength+1))
	require.Error(t, err)
	assert.Contains(t, err.Error(), fmt.Sprintf("%d characters long", MaxPrefixLength+1))
	assert.Contains(t, err.Error(), "exceeds maximum length")
	require.NoError(t, ValidatePrefix(strings.Repeat("a", MaxPrefixLength)))
}
