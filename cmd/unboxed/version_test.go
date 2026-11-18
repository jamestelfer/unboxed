package main

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// `--version` must print a non-empty string, including for a plain
// `go build` where no ldflags are injected.
func TestBuildVersionIsNeverEmpty(t *testing.T) {
	t.Cleanup(restoreBuildVars(version, commit, date))
	version, commit, date = "", "", ""

	require.NotEmpty(t, buildVersion())
}

func TestBuildVersionReportsInjectedBuildMetadata(t *testing.T) {
	t.Cleanup(restoreBuildVars(version, commit, date))
	version, commit, date = "1.2.3", "abc1234", "2026-08-30T00:00:00Z"

	got := buildVersion()
	for _, want := range []string{"1.2.3", "abc1234", "2026-08-30T00:00:00Z"} {
		assert.Contains(t, got, want)
	}
}

// The MCP server reports the bare release version, without the commit and
// date that `--version` appends.
func TestReleaseVersionOmitsBuildMetadata(t *testing.T) {
	t.Cleanup(restoreBuildVars(version, commit, date))
	version, commit, date = "1.2.3", "abc1234", "2026-08-30T00:00:00Z"

	assert.Equal(t, "1.2.3", releaseVersion())
}

func TestReleaseVersionFallsBackToBuildInfo(t *testing.T) {
	t.Cleanup(restoreBuildVars(version, commit, date))
	version, commit, date = "", "", ""

	assert.NotEmpty(t, releaseVersion())
	assert.Equal(t, buildVersion(), releaseVersion())
}

func restoreBuildVars(v, c, d string) func() {
	return func() { version, commit, date = v, c, d }
}
