package hookd

import (
	"runtime/debug"
	"strings"
)

// develVersion is what the build info reports for the main module of a local build.
const develVersion = "(devel)"

// moduleVersion returns the go-hookd version the running binary was built
// with, from the build info, or UserAgentVersion when that is unavailable.
func moduleVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return UserAgentVersion
	}
	return versionFromBuildInfo(info)
}

// versionFromBuildInfo picks go-hookd's version out of info: as a dependency
// (the only way it ships) or as the main module. A replaced dependency reports
// the replacement's version when it has one; a local path replacement has none.
func versionFromBuildInfo(info *debug.BuildInfo) string {
	candidates := []*debug.Module{&info.Main}
	candidates = append(candidates, info.Deps...)
	for _, mod := range candidates {
		if mod == nil || mod.Path != ModulePath {
			continue
		}
		version := mod.Version
		if mod.Replace != nil {
			version = mod.Replace.Version
		}
		if version == "" || version == develVersion {
			return UserAgentVersion
		}
		return strings.TrimPrefix(version, "v")
	}
	return UserAgentVersion
}
