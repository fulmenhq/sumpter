package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Fixtures and subprocesses are synthetic. No operator checker or policy is
// inherited, and each resolver branch runs with a deliberately bounded PATH.
type confidentialityFixture struct {
	root, hook, checker, observation, tools, caller string
}

func newConfidentialityFixture(t *testing.T, resolver string) confidentialityFixture {
	t.Helper()
	body, err := os.ReadFile("confidentiality-tree-check.sh")
	if err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	f := confidentialityFixture{
		root:        filepath.Join(base, "checkout"),
		checker:     filepath.Join(base, "checks with spaces", "check with spaces"),
		observation: filepath.Join(base, "observation"),
		tools:       filepath.Join(base, "tools"),
	}
	f.hook = filepath.Join(f.root, "scripts", "confidentiality-tree-check.sh")
	f.caller = f.root
	for _, path := range []string{filepath.Dir(f.hook), filepath.Dir(f.checker), f.tools} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	writeConfidentialityFile(t, f.hook, string(body), 0o700)
	writeConfidentialityFile(t, f.checker, `#!/bin/bash
printf '%s\n%s\n' "$PWD" "$#" > "$CHECK_OBSERVATION"
echo 'synthetic checker executed'
exit "$CHECK_STATUS"
`, 0o700)
	if resolver != "none" {
		tool, err := exec.LookPath(resolver)
		if err != nil {
			t.Skipf("%s unavailable: %v", resolver, err)
		}
		tool, err = filepath.Abs(tool)
		if err != nil {
			t.Fatal(err)
		}
		confidentialitySymlink(t, tool, filepath.Join(f.tools, resolver))
	}
	return f
}

func writeConfidentialityFile(t *testing.T, path, body string, mode os.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
}

func confidentialitySymlink(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
}

func (f confidentialityFixture) execute(t *testing.T, mode, checker *string, status int) (string, int) {
	t.Helper()
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skipf("bash unavailable: %v", err)
	}
	env := cleanEnv()
	for i, value := range env {
		if strings.HasPrefix(value, "PATH=") {
			env[i] = "PATH=" + f.tools
		}
	}
	env = append(env, "CHECK_OBSERVATION="+f.observation, fmt.Sprintf("CHECK_STATUS=%d", status))
	if mode != nil {
		env = append(env, "SUMPTER_REQUIRE_CONFIDENTIALITY_CHECK="+*mode)
	}
	if checker != nil {
		env = append(env, "SUMPTER_CONFIDENTIALITY_CHECK="+*checker)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bash, f.hook)
	cmd.Dir, cmd.Env = f.caller, env
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("hook did not terminate: %v", ctx.Err())
	}
	code := 0
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
		} else {
			t.Fatal(err)
		}
	}
	if strings.Contains(string(out), filepath.Dir(f.root)) {
		t.Fatalf("hook echoed a configured fixture path: %s", out)
	}
	return string(out), code
}

func assertConfidentialityInvocation(t *testing.T, f confidentialityFixture, invoked bool) {
	t.Helper()
	data, err := os.ReadFile(f.observation)
	if !invoked {
		if !os.IsNotExist(err) {
			t.Fatalf("checker invoked on failed admission or skip: %q, %v", data, err)
		}
		return
	}
	if err != nil {
		t.Fatal(err)
	}
	cwd, err := filepath.EvalSymlinks(f.caller)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != cwd+"\n0\n" {
		t.Fatalf("caller CWD/zero arguments not preserved: got %q, want %q", data, cwd+"\n0\n")
	}
}

func TestConfidentialityModeMatrix(t *testing.T) {
	for _, mode := range []*string{nil, strPtr("0"), strPtr("1"), strPtr(""), strPtr("true"), strPtr("false"), strPtr("01"), strPtr("2"), strPtr(" 1"), strPtr("1 "), strPtr("\n")} {
		name := "absent"
		if mode != nil {
			name = fmt.Sprintf("%q", *mode)
		}
		for _, configured := range []string{"absent", "empty", "executable"} {
			t.Run(name+"/"+configured, func(t *testing.T) {
				f := newConfidentialityFixture(t, "none")
				var checker *string
				switch configured {
				case "empty":
					checker = strPtr("")
				case "executable":
					checker = &f.checker
				}
				out, code := f.execute(t, mode, checker, 0)
				optional := mode == nil || *mode == "0"
				invalid := mode != nil && *mode != "0" && *mode != "1"
				skip := optional && configured != "executable"
				want := 1 // Configured calls also fail here: deliberately no resolver.
				if skip {
					want = 0
				}
				if code != want || strings.Contains(out, "SKIPPED") != skip || strings.Contains(out, "PASS") {
					t.Fatalf("mode outcome: code=%d, want=%d, output=%q", code, want, out)
				}
				if invalid && !strings.Contains(out, "invalid SUMPTER_REQUIRE_CONFIDENTIALITY_CHECK") {
					t.Fatalf("invalid mode was not checked first: %q", out)
				}
				assertConfidentialityInvocation(t, f, false)
			})
		}
	}
}

func strPtr(value string) *string { return &value }

func TestConfidentialityConfiguredMatrix(t *testing.T) {
	for _, resolver := range []string{"realpath", "python3"} {
		for _, mode := range []*string{nil, strPtr("0"), strPtr("1")} {
			name := "absent"
			if mode != nil {
				name = *mode
			}
			for _, status := range []int{0, 1, 2, 3, 17} {
				t.Run(fmt.Sprintf("%s/mode%s/exit%d", resolver, name, status), func(t *testing.T) {
					f := newConfidentialityFixture(t, resolver)
					out, code := f.execute(t, mode, &f.checker, status)
					if code != status || strings.Contains(out, "SKIPPED") {
						t.Fatalf("checker status not preserved: code=%d, want=%d, output=%q", code, status, out)
					}
					assertConfidentialityInvocation(t, f, true)
				})
			}
		}
	}
}

func TestConfidentialityPathAdmission(t *testing.T) {
	cases := []struct {
		name string
		path func(*testing.T, *confidentialityFixture) string
		pass bool
	}{
		{"relative path and different caller", func(t *testing.T, f *confidentialityFixture) string {
			f.caller = t.TempDir()
			relative, err := filepath.Rel(f.caller, f.checker)
			if err != nil {
				t.Fatal(err)
			}
			return relative
		}, true},
		{"shared prefix sibling", func(t *testing.T, f *confidentialityFixture) string {
			path := f.root + "-other-check"
			body, err := os.ReadFile(f.checker)
			if err != nil {
				t.Fatal(err)
			}
			writeConfidentialityFile(t, path, string(body), 0o700)
			return path
		}, true},
		{"outside multi-hop symlinks", func(t *testing.T, f *confidentialityFixture) string {
			one, two := f.checker+"-one", f.checker+"-two"
			confidentialitySymlink(t, f.checker, one)
			confidentialitySymlink(t, one, two)
			return two
		}, true},
		{"in-tree file", func(t *testing.T, f *confidentialityFixture) string {
			path := filepath.Join(f.root, "inside-check")
			writeConfidentialityFile(t, path, "#!/bin/bash\nexit 0\n", 0o700)
			return path
		}, false},
		{"outside symlink to in-tree", func(t *testing.T, f *confidentialityFixture) string {
			path := filepath.Join(f.root, "inside-check")
			writeConfidentialityFile(t, path, "#!/bin/bash\nexit 0\n", 0o700)
			confidentialitySymlink(t, path, f.checker+"-hop")
			confidentialitySymlink(t, f.checker+"-hop", f.checker+"-link")
			return f.checker + "-link"
		}, false},
		{"self", func(t *testing.T, f *confidentialityFixture) string { return f.hook }, false},
		{"outside symlink to self", func(t *testing.T, f *confidentialityFixture) string {
			confidentialitySymlink(t, f.hook, f.checker+"-link")
			return f.checker + "-link"
		}, false},
		{"checkout root", func(t *testing.T, f *confidentialityFixture) string { return f.root }, false},
		{"directory", func(t *testing.T, f *confidentialityFixture) string { return filepath.Dir(f.checker) }, false},
		{"missing", func(t *testing.T, f *confidentialityFixture) string { return f.checker + "-missing" }, false},
		{"non-executable", func(t *testing.T, f *confidentialityFixture) string {
			if err := os.Chmod(f.checker, 0o600); err != nil {
				t.Fatal(err)
			}
			return f.checker
		}, false},
		{"shell command string", func(t *testing.T, f *confidentialityFixture) string { return f.checker + " --option" }, false},
		{"symlink loop", func(t *testing.T, f *confidentialityFixture) string {
			one, two := f.checker+"-one", f.checker+"-two"
			confidentialitySymlink(t, two, one)
			confidentialitySymlink(t, one, two)
			return one
		}, false},
		{"dangling symlink", func(t *testing.T, f *confidentialityFixture) string {
			confidentialitySymlink(t, f.checker+"-missing", f.checker+"-link")
			return f.checker + "-link"
		}, false},
		{"multiline leaf symlink", func(t *testing.T, f *confidentialityFixture) string {
			path := f.checker + "\n"
			writeConfidentialityFile(t, path, "#!/bin/bash\nexit 0\n", 0o700)
			confidentialitySymlink(t, path, f.checker+"-link")
			return f.checker + "-link"
		}, false},
		{"hook symlink still rejects in-tree", func(t *testing.T, f *confidentialityFixture) string {
			alias := f.checker + "-hook"
			confidentialitySymlink(t, f.hook, alias)
			f.hook = alias
			path := filepath.Join(f.root, "inside-check")
			writeConfidentialityFile(t, path, "#!/bin/bash\nexit 0\n", 0o700)
			return path
		}, false},
	}
	for _, resolver := range []string{"realpath", "python3"} {
		for _, mode := range []string{"0", "1"} {
			for _, tc := range cases {
				t.Run(resolver+"/mode"+mode+"/"+tc.name, func(t *testing.T) {
					f := newConfidentialityFixture(t, resolver)
					path := tc.path(t, &f)
					out, code := f.execute(t, &mode, &path, 0)
					want := 1
					if tc.pass {
						want = 0
					}
					if code != want || strings.Contains(out, "SKIPPED") {
						t.Fatalf("admission: code=%d, want=%d, output=%q", code, want, out)
					}
					assertConfidentialityInvocation(t, f, tc.pass)
				})
			}
		}
	}
}

func TestConfidentialityResolverFailure(t *testing.T) {
	for _, result := range []string{"empty", "relative", "multiline", "missing target", "nonzero"} {
		for _, mode := range []string{"0", "1"} {
			t.Run(result+"/mode"+mode, func(t *testing.T) {
				f := newConfidentialityFixture(t, "none")
				body := "#!/bin/bash\n"
				switch result {
				case "empty":
					body += "printf '\\n'\n"
				case "relative":
					body += "printf 'relative-path\\n'\n"
				case "multiline":
					body += "printf '/first\\n/second\\n'\n"
				case "missing target":
					body += "printf '/synthetic-missing-target\\n'\n"
				case "nonzero":
					body += "echo \"$SUMPTER_CONFIDENTIALITY_CHECK\" >&2\nexit 17\n"
				}
				writeConfidentialityFile(t, filepath.Join(f.tools, "realpath"), body, 0o700)
				out, code := f.execute(t, &mode, &f.checker, 0)
				if code != 1 || strings.Contains(out, "SKIPPED") {
					t.Fatalf("resolver failure: code=%d, output=%q", code, out)
				}
				assertConfidentialityInvocation(t, f, false)
			})
		}
	}
}
