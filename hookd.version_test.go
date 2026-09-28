package hookd

import (
	"os"
	"regexp"
	"runtime/debug"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestUserAgentVersionMatchesManifest is the release gate for the fallback copy
// (go-hookd#2): it cannot drift from versions.yaml again unnoticed.
func TestUserAgentVersionMatchesManifest(t *testing.T) {
	raw, err := os.ReadFile("versions.yaml")
	require.NoError(t, err)
	m := regexp.MustCompile(`(?m)^project:\s*\n(?:\s+.*\n)*?\s+version:\s*"([^"]+)"`).FindSubmatch(raw)
	require.NotNil(t, m, "versions.yaml project.version not found")
	assert.Equal(t, string(m[1]), UserAgentVersion)
}

func TestVersionFromBuildInfo(t *testing.T) {
	tests := []struct {
		name string
		info *debug.BuildInfo
		want string
	}{
		{name: "dependency", info: &debug.BuildInfo{Deps: []*debug.Module{{Path: ModulePath, Version: "v0.11.0"}}}, want: "0.11.0"},
		{name: "pseudo-version", info: &debug.BuildInfo{Deps: []*debug.Module{{Path: ModulePath, Version: "v0.11.1-0.20260928120000-abcdef123456"}}}, want: "0.11.1-0.20260928120000-abcdef123456"},
		{name: "versioned replace", info: &debug.BuildInfo{Deps: []*debug.Module{{Path: ModulePath, Version: "v0.9.0", Replace: &debug.Module{Path: "example.com/fork", Version: "v0.11.2"}}}}, want: "0.11.2"},
		{name: "local path replace", info: &debug.BuildInfo{Deps: []*debug.Module{{Path: ModulePath, Version: "v0.9.0", Replace: &debug.Module{Path: "../go-hookd"}}}}, want: UserAgentVersion},
		{name: "main module devel", info: &debug.BuildInfo{Main: debug.Module{Path: ModulePath, Version: "(devel)"}}, want: UserAgentVersion},
		{name: "absent", info: &debug.BuildInfo{Main: debug.Module{Path: "example.com/app"}}, want: UserAgentVersion},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, versionFromBuildInfo(tt.info))
		})
	}
	assert.Equal(t, UserAgentPrefix+"/"+moduleVersion(), UserAgent)
}
