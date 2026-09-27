package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for release-tag.sh. They generate throwaway keys in a temporary gpg
// homedir and use a local bare repository as origin; no network and no real
// signing key are involved. Skipped when git or gpg is unavailable.

const (
	testTag         = "v1.2.3"
	testTaggerName  = "Release Tagger"
	testTaggerEmail = "tagger@example.test"
)

type releaseKeys struct {
	home    string
	primary string
	signA   string
	signB   string
	foreign string
}

// releaseTagTools are the external tools the release-tag tests need.
var releaseTagTools = []string{"git", "gpg", "gpgconf", "bash"}

// requireTools skips the test when a tool is missing, or fails it when
// SUMPTER_REQUIRE_CRYPTO_TOOLS=1 (set by the CI job that must run these tests).
func requireTools(t *testing.T, tools ...string) {
	t.Helper()
	for _, tool := range tools {
		if _, err := exec.LookPath(tool); err != nil {
			if os.Getenv("SUMPTER_REQUIRE_CRYPTO_TOOLS") == "1" {
				t.Fatalf("%s unavailable and SUMPTER_REQUIRE_CRYPTO_TOOLS=1: %v", tool, err)
			}
			t.Skipf("%s unavailable: %v", tool, err)
		}
	}
}

func run(t *testing.T, dir string, env []string, name string, args ...string) string {
	t.Helper()
	out, err := runErr(dir, env, name, args...)
	if err != nil {
		t.Fatalf("%s %s: %v\n%s", name, strings.Join(args, " "), err, out)
	}
	return out
}

func runErr(dir string, env []string, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = env
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf
	err := cmd.Run()
	return buf.String(), err
}

// cleanEnv drops ambient SUMPTER_*, GNUPGHOME, and GIT_* variables so the
// operator's release environment can never leak into a test.
func cleanEnv() []string {
	var env []string
	for _, kv := range os.Environ() {
		key := kv[:strings.IndexByte(kv, '=')]
		if strings.HasPrefix(key, "SUMPTER_") || strings.HasPrefix(key, "GIT_") || key == "GNUPGHOME" {
			continue
		}
		env = append(env, kv)
	}
	return append(env, "GIT_CONFIG_NOSYSTEM=1", "GIT_TERMINAL_PROMPT=0")
}

func fingerprints(t *testing.T, home, selector string) []string {
	t.Helper()
	out := run(t, "", cleanEnv(), "gpg", "--homedir", home, "--batch", "--with-colons", "--fixed-list-mode", "--list-keys", selector)
	var fprs []string
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(line, ":")
		if len(fields) > 9 && fields[0] == "fpr" {
			fprs = append(fprs, fields[9])
		}
	}
	return fprs
}

func newReleaseKeys(t *testing.T) releaseKeys {
	t.Helper()
	// Short path: gpg-agent sockets live in the homedir and macOS limits
	// socket paths to ~104 bytes, which t.TempDir() can exceed.
	home, err := os.MkdirTemp("/tmp", "rtag-gpg-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	if err := os.Chmod(home, 0o700); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() {
		_, _ = runErr("", cleanEnv(), "gpgconf", "--homedir", home, "--kill", "all")
		_ = os.RemoveAll(home)
	})
	gpg := func(args ...string) {
		base := []string{"--homedir", home, "--batch", "--pinentry-mode", "loopback", "--passphrase", ""}
		run(t, "", cleanEnv(), "gpg", append(base, args...)...)
	}

	gpg("--quick-gen-key", testTaggerName+" <"+testTaggerEmail+">", "ed25519", "cert", "never")
	primary := fingerprints(t, home, testTaggerEmail)[0]
	gpg("--quick-add-key", primary, "ed25519", "sign", "never")
	gpg("--quick-add-key", primary, "ed25519", "sign", "never")
	fprs := fingerprints(t, home, primary)
	if len(fprs) != 3 {
		t.Fatalf("expected primary + 2 subkeys, got %v", fprs)
	}
	gpg("--quick-gen-key", "Foreign Signer <foreign@example.test>", "ed25519", "sign", "never")
	foreign := fingerprints(t, home, "foreign@example.test")[0]

	return releaseKeys{home: home, primary: fprs[0], signA: fprs[1], signB: fprs[2], foreign: foreign}
}

type releaseRepo struct {
	t      *testing.T
	keys   releaseKeys
	origin string
	clone  string
	script string
	vars   map[string]string
}

func newReleaseRepo(t *testing.T, keys releaseKeys) *releaseRepo {
	t.Helper()
	script, err := filepath.Abs("release-tag.sh")
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	root := t.TempDir()
	origin := filepath.Join(root, "origin.git")
	clone := filepath.Join(root, "clone")
	env := cleanEnv()
	run(t, root, env, "git", "init", "--quiet", "--bare", "--initial-branch=main", origin)
	run(t, root, env, "git", "clone", "--quiet", origin, clone)
	run(t, clone, env, "git", "config", "user.name", "Ambient Committer")
	run(t, clone, env, "git", "config", "user.email", "ambient@example.test")
	run(t, clone, env, "git", "config", "commit.gpgsign", "false")
	run(t, clone, env, "git", "checkout", "--quiet", "-b", "main")
	if err := os.WriteFile(filepath.Join(clone, "VERSION"), []byte("1.2.3\n"), 0o600); err != nil {
		t.Fatalf("WriteFile VERSION: %v", err)
	}
	run(t, clone, env, "git", "add", "VERSION")
	run(t, clone, env, "git", "commit", "--quiet", "-m", "init")
	run(t, clone, env, "git", "push", "--quiet", "origin", "main")

	return &releaseRepo{
		t: t, keys: keys, origin: origin, clone: clone, script: script,
		vars: map[string]string{
			"SUMPTER_RELEASE_TAG":  testTag,
			"SUMPTER_PGP_KEY_ID":   keys.signA + "!",
			"SUMPTER_GPG_HOMEDIR":  keys.home,
			"SUMPTER_TAGGER_NAME":  testTaggerName,
			"SUMPTER_TAGGER_EMAIL": testTaggerEmail,
		},
	}
}

func (r *releaseRepo) env() []string {
	env := cleanEnv()
	for k, v := range r.vars {
		if v != "" {
			env = append(env, k+"="+v)
		}
	}
	return env
}

func (r *releaseRepo) runScript(cmd string) (string, error) {
	return runErr(r.clone, r.env(), "bash", r.script, cmd)
}

func (r *releaseRepo) git(args ...string) string {
	r.t.Helper()
	return run(r.t, r.clone, cleanEnv(), "git", args...)
}

func (r *releaseRepo) localTagExists() bool {
	return strings.TrimSpace(r.git("tag", "--list", testTag)) != ""
}

func (r *releaseRepo) remoteTagExists() bool {
	return strings.TrimSpace(r.git("ls-remote", "--tags", "origin", "refs/tags/"+testTag)) != ""
}

// signWith creates the release tag by hand, signed by an arbitrary key with the
// declared tagger identity, to exercise verify against the wrong signer.
func (r *releaseRepo) signWith(keySelector string) {
	r.t.Helper()
	env := append(cleanEnv(),
		"GNUPGHOME="+r.keys.home,
		"GIT_COMMITTER_NAME="+testTaggerName,
		"GIT_COMMITTER_EMAIL="+testTaggerEmail,
	)
	run(r.t, r.clone, env, "git", "-c", "gpg.program=gpg", "tag", "-s", "-u", keySelector, testTag, "-m", "Release "+testTag)
}

func expectFail(t *testing.T, out string, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected failure containing %q, got success:\n%s", want, out)
	}
	if !strings.Contains(out, want) {
		t.Fatalf("expected output containing %q, got:\n%s", want, out)
	}
	if strings.Contains(out, "verified: ") || strings.Contains(out, "created signed local tag") {
		t.Fatalf("failure path printed a success line:\n%s", out)
	}
}

func TestReleaseTagGuards(t *testing.T) {
	requireTools(t, releaseTagTools...)
	keys := newReleaseKeys(t)

	for _, name := range []string{"SUMPTER_RELEASE_TAG", "SUMPTER_PGP_KEY_ID", "SUMPTER_GPG_HOMEDIR", "SUMPTER_TAGGER_NAME", "SUMPTER_TAGGER_EMAIL"} {
		t.Run("missing "+name, func(t *testing.T) {
			r := newReleaseRepo(t, keys)
			r.vars[name] = ""
			out, err := r.runScript("create")
			expectFail(t, out, err, name+" is required")
			if r.localTagExists() {
				t.Fatal("tag created despite missing variable")
			}
		})
	}

	cases := []struct {
		name  string
		setup func(r *releaseRepo)
		want  string
	}{
		{"tag does not match VERSION", func(r *releaseRepo) { r.vars["SUMPTER_RELEASE_TAG"] = "v9.9.9" }, "does not match VERSION"},
		{"dirty tree", func(r *releaseRepo) {
			if err := os.WriteFile(filepath.Join(r.clone, "VERSION"), []byte("1.2.3\n\n"), 0o600); err != nil {
				t.Fatalf("WriteFile: %v", err)
			}
		}, "working tree is not clean"},
		{"not on main", func(r *releaseRepo) { r.git("checkout", "--quiet", "-b", "other") }, "release tags are created from main"},
		{"HEAD ahead of origin/main", func(r *releaseRepo) { r.git("commit", "--quiet", "--allow-empty", "-m", "local only") }, "does not match origin/main"},
		{"tag exists locally", func(r *releaseRepo) { r.git("tag", testTag) }, "already exists locally"},
		{"tag exists on origin", func(r *releaseRepo) {
			r.git("push", "--quiet", "origin", "HEAD:refs/tags/"+testTag)
		}, "already exists on origin"},
		{"tagger email not a uid", func(r *releaseRepo) { r.vars["SUMPTER_TAGGER_EMAIL"] = "someone@example.test" }, "not a uid email"},
		{"short key id rejected", func(r *releaseRepo) { r.vars["SUMPTER_PGP_KEY_ID"] = keys.signA[32:] }, "must be a 40-hex fingerprint or 16-hex long key id"},
		{"email selector rejected", func(r *releaseRepo) { r.vars["SUMPTER_PGP_KEY_ID"] = testTaggerEmail }, "must be a 40-hex fingerprint or 16-hex long key id"},
		{"unknown key", func(r *releaseRepo) { r.vars["SUMPTER_PGP_KEY_ID"] = strings.Repeat("A", 40) }, "does not match any key"},
		{"unforced subkey rejected", func(r *releaseRepo) { r.vars["SUMPTER_PGP_KEY_ID"] = keys.signA }, "names a subkey"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newReleaseRepo(t, keys)
			tc.setup(r)
			out, err := r.runScript("create")
			expectFail(t, out, err, tc.want)
			if tc.name != "tag exists locally" && tc.name != "tag exists on origin" && r.localTagExists() {
				t.Fatal("tag created despite failed guard")
			}
		})
	}

	t.Run("tagger email failure lists key uids", func(t *testing.T) {
		r := newReleaseRepo(t, keys)
		r.vars["SUMPTER_TAGGER_EMAIL"] = "someone@example.test"
		out, _ := r.runScript("create")
		if !strings.Contains(out, testTaggerName+" <"+testTaggerEmail+">") {
			t.Fatalf("uid list missing from output:\n%s", out)
		}
	})

	t.Run("ambiguous selector", func(t *testing.T) {
		r := newReleaseRepo(t, keys)
		// A real 64-bit key id collision cannot be generated, so a gpg shim
		// reports two keys that share the selected long id.
		shimDir := t.TempDir()
		longID := keys.signA[24:]
		listing := strings.Join([]string{
			"pub:u:255:22:" + keys.primary[24:] + ":1:::u:::cC::::::23::0:",
			"fpr:::::::::" + keys.primary + ":",
			"sub:u:255:22:" + longID + ":1::::::s::::::23:",
			"fpr:::::::::" + keys.signA + ":",
			"pub:u:255:22:" + longID + ":1:::u:::scSC::::::23::0:",
			"fpr:::::::::" + strings.Repeat("B", 24) + longID + ":",
		}, "\n")
		shim := "#!/usr/bin/env bash\ncat <<'EOF'\n" + listing + "\nEOF\n"
		if err := os.WriteFile(filepath.Join(shimDir, "gpg"), []byte(shim), 0o700); err != nil {
			t.Fatalf("WriteFile shim: %v", err)
		}
		r.vars["SUMPTER_PGP_KEY_ID"] = longID + "!"
		env := r.env()
		for i, kv := range env {
			if strings.HasPrefix(kv, "PATH=") {
				env[i] = "PATH=" + shimDir + ":" + kv[len("PATH="):]
			}
		}
		out, err := runErr(r.clone, env, "bash", r.script, "create")
		expectFail(t, out, err, "is ambiguous")
		if r.localTagExists() {
			t.Fatal("tag created despite ambiguous selector")
		}
	})
}

func TestReleaseTagSignatureChecks(t *testing.T) {
	requireTools(t, releaseTagTools...)
	keys := newReleaseKeys(t)

	t.Run("forced key rejects sibling subkey of same primary", func(t *testing.T) {
		r := newReleaseRepo(t, keys)
		r.signWith(keys.signB + "!")
		out, err := r.runScript("verify")
		expectFail(t, out, err, "not the selected key")
	})

	t.Run("forced key rejects foreign key", func(t *testing.T) {
		r := newReleaseRepo(t, keys)
		r.signWith(keys.foreign)
		out, err := r.runScript("verify")
		expectFail(t, out, err, "not the selected key")
	})

	t.Run("unforced primary rejects foreign key", func(t *testing.T) {
		r := newReleaseRepo(t, keys)
		r.vars["SUMPTER_PGP_KEY_ID"] = keys.primary
		r.signWith(keys.foreign)
		out, err := r.runScript("verify")
		expectFail(t, out, err, "was signed under primary")
	})

	t.Run("tampered tag fails", func(t *testing.T) {
		r := newReleaseRepo(t, keys)
		r.signWith(keys.signA + "!")
		obj := r.git("cat-file", "tag", testTag)
		tampered := strings.Replace(obj, "Release "+testTag, "Release v1.2.4", 1)
		cmd := exec.Command("git", "hash-object", "-t", "tag", "-w", "--stdin")
		cmd.Dir = r.clone
		cmd.Env = cleanEnv()
		cmd.Stdin = strings.NewReader(tampered)
		sha, err := cmd.Output()
		if err != nil {
			t.Fatalf("hash-object: %v", err)
		}
		r.git("update-ref", "refs/tags/"+testTag, strings.TrimSpace(string(sha)))
		out, err := r.runScript("verify")
		expectFail(t, out, err, "git verify-tag failed")

		out, err = r.runScript("push")
		expectFail(t, out, err, "git verify-tag failed")
		if r.remoteTagExists() {
			t.Fatal("push published a tag that failed verification")
		}
	})

	t.Run("verbatim message without trailing newline fails", func(t *testing.T) {
		// Regression: with --cleanup=verbatim and no trailing newline, git
		// appends the signature to the message line and cannot find it.
		r := newReleaseRepo(t, keys)
		env := append(cleanEnv(),
			"GNUPGHOME="+keys.home,
			"GIT_COMMITTER_NAME="+testTaggerName,
			"GIT_COMMITTER_EMAIL="+testTaggerEmail,
		)
		run(t, r.clone, env, "git", "-c", "gpg.program=gpg", "tag", "-s", "-u", keys.signA+"!",
			"--cleanup=verbatim", testTag, "-m", "Release "+testTag)
		if obj := r.git("cat-file", "-p", testTag); !strings.Contains(obj, "Release "+testTag+"-----BEGIN PGP SIGNATURE-----") {
			t.Fatalf("fixture did not reproduce the joined signature layout:\n%s", obj)
		}
		out, err := r.runScript("verify")
		expectFail(t, out, err, "git verify-tag failed")

		out, err = r.runScript("push")
		expectFail(t, out, err, "git verify-tag failed")
		if r.remoteTagExists() {
			t.Fatal("push published a tag that failed verification")
		}
	})

	t.Run("unsigned annotated tag fails", func(t *testing.T) {
		r := newReleaseRepo(t, keys)
		env := append(cleanEnv(), "GIT_COMMITTER_NAME="+testTaggerName, "GIT_COMMITTER_EMAIL="+testTaggerEmail)
		run(t, r.clone, env, "git", "tag", "-a", testTag, "-m", "Release "+testTag)
		out, err := r.runScript("verify")
		expectFail(t, out, err, "git verify-tag failed")
	})

	t.Run("lightweight tag fails", func(t *testing.T) {
		r := newReleaseRepo(t, keys)
		r.git("tag", testTag)
		out, err := r.runScript("verify")
		expectFail(t, out, err, "is not an annotated tag object")
	})
}

func TestReleaseTagHappyPath(t *testing.T) {
	requireTools(t, releaseTagTools...)
	keys := newReleaseKeys(t)

	t.Run("forced signing subkey: create, verify, push", func(t *testing.T) {
		r := newReleaseRepo(t, keys)
		out, err := r.runScript("create")
		if err != nil {
			t.Fatalf("create: %v\n%s", err, out)
		}
		if !strings.Contains(out, "signer:  "+keys.signA) || !strings.Contains(out, "primary: "+keys.primary) {
			t.Fatalf("verify output missing expected fingerprints:\n%s", out)
		}
		if got := r.git("cat-file", "-t", testTag); strings.TrimSpace(got) != "tag" {
			t.Fatalf("tag type = %q, want tag", got)
		}
		obj := r.git("cat-file", "-p", testTag)
		if !strings.Contains(obj, "\ntagger "+testTaggerName+" <"+testTaggerEmail+"> ") {
			t.Fatalf("tagger line does not carry the declared identity:\n%s", obj)
		}
		if !strings.Contains(obj, "\n\nRelease "+testTag+"\n-----BEGIN PGP SIGNATURE-----") {
			t.Fatalf("tag message/signature layout unexpected:\n%s", obj)
		}

		out, err = r.runScript("push")
		if err != nil {
			t.Fatalf("push: %v\n%s", err, out)
		}
		local := strings.TrimSpace(r.git("rev-parse", "refs/tags/"+testTag))
		remote := strings.Fields(r.git("ls-remote", "--tags", "origin", "refs/tags/"+testTag))[0]
		if local != remote {
			t.Fatalf("remote tag %s != local tag %s", remote, local)
		}
		heads := r.git("ls-remote", "--heads", "origin")
		if strings.Count(strings.TrimSpace(heads), "\n") != 0 {
			t.Fatalf("push touched branches:\n%s", heads)
		}
	})

	t.Run("forced long key id", func(t *testing.T) {
		r := newReleaseRepo(t, keys)
		r.vars["SUMPTER_PGP_KEY_ID"] = strings.ToLower(keys.signA[24:]) + "!"
		out, err := r.runScript("create")
		if err != nil {
			t.Fatalf("create: %v\n%s", err, out)
		}
		if !strings.Contains(out, "signer:  "+keys.signA) {
			t.Fatalf("expected signer %s:\n%s", keys.signA, out)
		}
	})

	t.Run("unforced primary signs with a live subkey", func(t *testing.T) {
		r := newReleaseRepo(t, keys)
		r.vars["SUMPTER_PGP_KEY_ID"] = keys.primary
		out, err := r.runScript("create")
		if err != nil {
			t.Fatalf("create: %v\n%s", err, out)
		}
		if !strings.Contains(out, "signer:  "+keys.signA) && !strings.Contains(out, "signer:  "+keys.signB) {
			t.Fatalf("signer is not one of the primary's signing subkeys:\n%s", out)
		}
	})
}
