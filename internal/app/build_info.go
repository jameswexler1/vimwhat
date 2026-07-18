package app

import (
	"fmt"
	"io"
	"runtime/debug"
	"strings"
)

type BuildInfo struct {
	Version  string
	Revision string
	Time     string
	Modified bool
}

func currentBuildInfo() BuildInfo {
	result := BuildInfo{Version: "devel"}
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return result
	}
	if version := strings.TrimSpace(info.Main.Version); version != "" && version != "(devel)" {
		result.Version = version
	}
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			result.Revision = strings.TrimSpace(setting.Value)
		case "vcs.time":
			result.Time = strings.TrimSpace(setting.Value)
		case "vcs.modified":
			result.Modified = strings.EqualFold(strings.TrimSpace(setting.Value), "true")
		}
	}
	return result
}

func formatBuildInfo(info BuildInfo) string {
	version := strings.TrimSpace(info.Version)
	if version == "" {
		version = "devel"
	}
	parts := []string{"version=" + version}
	if info.Revision != "" {
		parts = append(parts, "revision="+info.Revision)
	}
	if info.Time != "" {
		parts = append(parts, "time="+info.Time)
	}
	if info.Modified {
		parts = append(parts, "modified=true")
	}
	return strings.Join(parts, " ")
}

func printVersion(w io.Writer) {
	fmt.Fprintf(w, "vimwhat %s\n", formatBuildInfo(currentBuildInfo()))
}

func isVersionCommand(args []string) bool {
	if len(args) != 1 {
		return false
	}
	switch args[0] {
	case "version", "-v", "--version":
		return true
	default:
		return false
	}
}
