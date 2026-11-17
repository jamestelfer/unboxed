package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRunPrintsGreetingAndSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"unboxed"}, &stdout, &stderr)

	require.Equal(t, exitOK, code, "stderr: %s", stderr.String())
	assert.Equal(t, "hello from unboxed\n", stdout.String())
}

func TestRunVersionFlagPrintsVersionAndSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"unboxed", "--version"}, &stdout, &stderr)

	require.Equal(t, exitOK, code, "stderr: %s", stderr.String())
	assert.NotEmpty(t, strings.TrimSpace(stdout.String()))
	assert.Contains(t, stdout.String(), buildVersion())
}

func TestRunHelpFlagPrintsUsageAndSucceeds(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"unboxed", "--help"}, &stdout, &stderr)

	require.Equal(t, exitOK, code, "stderr: %s", stderr.String())
	assert.Contains(t, stdout.String(), "unboxed")
}

func TestRunRejectsPositionalArgumentsAsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"unboxed", "extra"}, &stdout, &stderr)

	assert.Equal(t, exitUsage, code)
	assert.Contains(t, stderr.String(), "takes no arguments")
}

func TestRunRejectsUnknownFlagAsUsageError(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := run([]string{"unboxed", "--no-such-flag"}, &stdout, &stderr)

	assert.Equal(t, exitUsage, code)
}
