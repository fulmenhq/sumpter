package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

const toolchainTestVersion = "1.26.10"

type toolchainFixture struct {
	root   string
	script string
	tools  string
}

func newToolchainFixture(t *testing.T, goMod string) toolchainFixture {
	t.Helper()
	source, err := os.ReadFile("toolchain-contract.sh")
	if err != nil {
		t.Fatal(err)
	}

	root := t.TempDir()
	f := toolchainFixture{
		root:   root,
		script: filepath.Join(root, "scripts", "toolchain-contract.sh"),
		tools:  filepath.Join(root, "tools"),
	}
	for _, dir := range []string{filepath.Dir(f.script), filepath.Join(root, "config"), f.tools} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	toolchainWriteFile(t, f.script, source, 0o700)
	toolchainWriteFile(t, filepath.Join(root, "go.mod"), []byte(goMod), 0o600)
	toolchainWriteFile(t, filepath.Join(root, "config", "toolchain.env"), []byte("SUMPTER_GO_VERSION="+toolchainTestVersion+"\nSUMPTER_GOLANGCI_LINT_VERSION=v2.14.0\nSUMPTER_EXPECTED_GOFLAGS=\n"), 0o600)
	return f
}

func toolchainWriteFile(t *testing.T, path string, data []byte, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, data, mode); err != nil {
		t.Fatal(err)
	}
}

func (f toolchainFixture) fakeGo(t *testing.T, version, goflags string) {
	t.Helper()
	toolchainWriteFile(t, filepath.Join(f.tools, "go"), []byte("#!/usr/bin/env bash\nset -euo pipefail\ncase \"${1:-} ${2:-}\" in\n'version ') printf 'go version go%s synthetic/amd64\\n' \"${FAKE_GO_VERSION:?}\" ;;\n'env GOFLAGS') printf '%s\\n' \"${FAKE_GOFLAGS:-}\" ;;\n*) printf 'unexpected fake go invocation: %s %s\\n' \"${1:-}\" \"${2:-}\" >&2; exit 64 ;;\nesac\n"), 0o700)
}

func (f toolchainFixture) checkGo(t *testing.T, version, goflags string) (string, error) {
	t.Helper()
	f.fakeGo(t, version, goflags)
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash unavailable: %v", err)
	}
	cmd := exec.Command(bash, f.script, "check-go")
	cmd.Dir = f.root
	cmd.Env = toolchainTestEnv(f.tools, version, goflags)
	var output bytes.Buffer
	cmd.Stdout, cmd.Stderr = &output, &output
	err = cmd.Run()
	return output.String(), err
}

func toolchainTestEnv(tools, version, goflags string) []string {
	env := make([]string, 0, len(os.Environ())+4)
	for _, entry := range os.Environ() {
		key, _, _ := strings.Cut(entry, "=")
		if key == "PATH" || key == "GOFLAGS" || strings.HasPrefix(key, "SUMPTER_") || key == "FAKE_GO_VERSION" || key == "FAKE_GOFLAGS" {
			continue
		}
		env = append(env, entry)
	}
	return append(env,
		"PATH="+tools+":"+os.Getenv("PATH"),
		"FAKE_GO_VERSION="+version,
		"FAKE_GOFLAGS="+goflags,
	)
}

func TestToolchainContractCheckGo(t *testing.T) {
	matchingGoMod := "module example.test/toolchain\n\ngo " + toolchainTestVersion + "\n"
	cases := []struct {
		name     string
		goMod    string
		actualGo string
		goflags  string
		wantErr  string
	}{
		{
			name:     "implicit toolchain uses selected go version",
			goMod:    matchingGoMod,
			actualGo: toolchainTestVersion,
		},
		{
			name:     "explicit matching toolchain accepts",
			goMod:    matchingGoMod + "\ntoolchain go" + toolchainTestVersion + "\n",
			actualGo: toolchainTestVersion,
		},
		{
			name:     "omitted toolchain with older go directive rejects",
			goMod:    "module example.test/toolchain\n\ngo 1.26.9\n",
			actualGo: toolchainTestVersion,
			wantErr:  "go.mod toolchain mismatch",
		},
		{
			name:     "explicit mismatched toolchain rejects",
			goMod:    matchingGoMod + "\ntoolchain go1.26.9\n",
			actualGo: toolchainTestVersion,
			wantErr:  "go.mod toolchain mismatch",
		},
		{
			name:     "actual host mismatch rejects",
			goMod:    matchingGoMod,
			actualGo: "1.27.3",
			wantErr:  "Go toolchain mismatch",
		},
		{
			name:     "unexpected goflags rejects",
			goMod:    matchingGoMod,
			actualGo: toolchainTestVersion,
			goflags:  "-mod=mod",
			wantErr:  "GOFLAGS mismatch",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fixture := newToolchainFixture(t, tc.goMod)
			output, err := fixture.checkGo(t, tc.actualGo, tc.goflags)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("check-go failed: %v\n%s", err, output)
				}
				if !strings.Contains(output, "toolchain-contract: go ok: go"+toolchainTestVersion) {
					t.Fatalf("missing success message: %s", output)
				}
				return
			}
			if err == nil || !strings.Contains(output, tc.wantErr) {
				t.Fatalf("check-go error = %v, output = %q; want %q", err, output, tc.wantErr)
			}
		})
	}
}
