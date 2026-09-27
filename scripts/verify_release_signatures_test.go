package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for verify-release-signatures.sh. Keys are generated per run with
// minisign and gpg in temporary directories; no real signing key is used.

const fixtureName = "fixture"

var verifySignaturesTools = []string{"bash", "gpg", "gpgconf", "minisign"}

type signingFixture struct {
	minisignPub   string
	minisignSec   string
	wrongSec      string
	gpgHome       string
	signer        string // single-primary signing key that gets exported
	other         string // second primary: foreign signer / bundle member
	expired       string // primary whose validity ended in 2020
	revoked       string // primary carrying a revocation
	revokedExport string // armored export of the revoked key
}

func gpgFixture(t *testing.T, home string, args ...string) string {
	t.Helper()
	base := []string{"--homedir", home, "--batch", "--pinentry-mode", "loopback", "--passphrase", ""}
	return run(t, "", cleanEnv(), "gpg", append(base, args...)...)
}

// gpgExport returns only gpg's stdout (the armored key), never diagnostics.
func gpgExport(t *testing.T, home string, keys ...string) string {
	t.Helper()
	args := append([]string{"--homedir", home, "--batch", "--armor", "--export"}, keys...)
	cmd := exec.Command("gpg", args...)
	cmd.Env = cleanEnv()
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("gpg --export: %v", err)
	}
	return string(out)
}

func newSigningFixture(t *testing.T) signingFixture {
	t.Helper()
	keys := t.TempDir()
	f := signingFixture{
		minisignPub: filepath.Join(keys, "good.pub"),
		minisignSec: filepath.Join(keys, "good.key"),
		wrongSec:    filepath.Join(keys, "wrong.key"),
	}
	run(t, keys, cleanEnv(), "minisign", "-G", "-W", "-f", "-p", f.minisignPub, "-s", f.minisignSec)
	run(t, keys, cleanEnv(), "minisign", "-G", "-W", "-f", "-p", filepath.Join(keys, "wrong.pub"), "-s", f.wrongSec)

	// Short path: gpg-agent sockets live in the homedir (see release_tag_test.go).
	home, err := os.MkdirTemp("/tmp", "vsig-gpg-")
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
	f.gpgHome = home

	gen := func(uid string, extra ...string) string {
		gpgFixture(t, home, append(extra, "--quick-gen-key", uid, "ed25519", "sign", "never")...)
		return fingerprints(t, home, uid)[0]
	}
	f.signer = gen("Fixture Signer <signer@example.test>")
	f.other = gen("Other Signer <other@example.test>")

	gpgFixture(t, home, "--faked-system-time", "20200101T000000!",
		"--quick-gen-key", "Expired Signer <expired@example.test>", "ed25519", "sign", "1d")
	f.expired = fingerprints(t, home, "expired@example.test")[0]

	f.revoked = gen("Revoked Signer <revoked@example.test>")
	return f
}

// releaseDir builds a complete, validly signed release directory: both
// manifests, both signature families, and the exported public keys.
func (f *signingFixture) releaseDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	for _, m := range []string{"SHA256SUMS", "SHA512SUMS"} {
		if err := os.WriteFile(filepath.Join(dir, m), []byte("0123abcd  "+fixtureName+"-linux-amd64\n"), 0o600); err != nil {
			t.Fatalf("WriteFile %s: %v", m, err)
		}
		f.minisignSign(t, f.minisignSec, dir, m)
		f.pgpSign(t, f.signer, dir, m)
	}
	data, err := os.ReadFile(f.minisignPub)
	if err != nil {
		t.Fatalf("ReadFile pub: %v", err)
	}
	writeFile(t, dir, fixtureName+"-minisign.pub", string(data))
	writeFile(t, dir, fixtureName+"-release-signing-key.asc", gpgExport(t, f.gpgHome, f.signer))
	return dir
}

func (f *signingFixture) minisignSign(t *testing.T, sec, dir, manifest string) {
	t.Helper()
	path := filepath.Join(dir, manifest)
	run(t, dir, cleanEnv(), "minisign", "-S", "-s", sec, "-m", path, "-x", path+".minisig", "-t", "fixture")
}

func (f *signingFixture) pgpSign(t *testing.T, key, dir, manifest string, extra ...string) {
	t.Helper()
	path := filepath.Join(dir, manifest)
	args := append(extra, "--yes", "--armor", "--local-user", key, "--detach-sign", "-o", path+".asc", path)
	gpgFixture(t, f.gpgHome, args...)
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile %s: %v", name, err)
	}
}

func appendFile(t *testing.T, dir, name, content string) {
	t.Helper()
	fh, err := os.OpenFile(filepath.Join(dir, name), os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatalf("OpenFile %s: %v", name, err)
	}
	defer func() { _ = fh.Close() }()
	if _, err := fh.WriteString(content); err != nil {
		t.Fatalf("append %s: %v", name, err)
	}
}

func removeFile(t *testing.T, dir, name string) {
	t.Helper()
	if err := os.Remove(filepath.Join(dir, name)); err != nil {
		t.Fatalf("Remove %s: %v", name, err)
	}
}

// verifyRun runs the verifier with a private TMPDIR and asserts that the
// temporary keyring it may create is gone afterwards.
func verifyRun(t *testing.T, dir string, pathPrefix string) (string, error) {
	t.Helper()
	script, err := filepath.Abs("verify-release-signatures.sh")
	if err != nil {
		t.Fatalf("Abs: %v", err)
	}
	// Short TMPDIR: the verifier's temporary gpg homedir holds agent sockets,
	// and t.TempDir() paths (which embed the subtest name) can exceed the
	// socket path limit, making gpg fail (closed) before it verifies anything.
	tmp, err := os.MkdirTemp("/tmp", "vsig-tmp-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmp) })
	env := append(cleanEnv(), "TMPDIR="+tmp)
	if pathPrefix != "" {
		for i, kv := range env {
			if strings.HasPrefix(kv, "PATH=") {
				env[i] = "PATH=" + pathPrefix + ":" + kv[len("PATH="):]
			}
		}
	}
	out, verifyErr := runErr(dir, env, "bash", script, dir, fixtureName)
	left, err := os.ReadDir(tmp)
	if err != nil {
		t.Fatalf("ReadDir TMPDIR: %v", err)
	}
	if len(left) != 0 {
		t.Fatalf("temporary files left behind: %v\n%s", left, out)
	}
	return out, verifyErr
}

func expectVerifyFail(t *testing.T, out string, err error, want string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected failure containing %q, got success:\n%s", want, out)
	}
	if !strings.Contains(out, want) {
		t.Fatalf("expected output containing %q, got:\n%s", want, out)
	}
	if strings.Contains(out, "✅") {
		t.Fatalf("failure path printed a success line:\n%s", out)
	}
}

func TestVerifyReleaseSignatures(t *testing.T) {
	requireTools(t, verifySignaturesTools...)
	f := newSigningFixture(t)

	// Sign one manifest with the expired key at a time it was still valid,
	// and give the revoked key its revocation before export.
	revFile := filepath.Join(f.gpgHome, "openpgp-revocs.d", f.revoked+".rev")
	revCert, err := os.ReadFile(revFile)
	if err != nil {
		t.Fatalf("ReadFile revocation: %v", err)
	}

	t.Run("happy path: both families", func(t *testing.T) {
		dir := f.releaseDir(t)
		out, err := verifyRun(t, dir, "")
		if err != nil {
			t.Fatalf("verify: %v\n%s", err, out)
		}
		for _, want := range []string{"✅ Minisign signatures verified", "✅ PGP signatures verified"} {
			if !strings.Contains(out, want) {
				t.Fatalf("missing %q:\n%s", want, out)
			}
		}
	})

	t.Run("happy path: minisign only never invokes gpg", func(t *testing.T) {
		dir := f.releaseDir(t)
		removeFile(t, dir, "SHA256SUMS.asc")
		removeFile(t, dir, "SHA512SUMS.asc")
		shim := t.TempDir()
		marker := filepath.Join(shim, "gpg-called")
		for _, tool := range []string{"gpg", "gpgconf"} {
			writeFile(t, shim, tool, "#!/usr/bin/env bash\ntouch "+marker+"\nexit 1\n")
			if err := os.Chmod(filepath.Join(shim, tool), 0o700); err != nil {
				t.Fatalf("Chmod shim: %v", err)
			}
		}
		out, err := verifyRun(t, dir, shim)
		if err != nil {
			t.Fatalf("verify: %v\n%s", err, out)
		}
		if !strings.Contains(out, "✅ Minisign signatures verified") || strings.Contains(out, "PGP signatures verified") {
			t.Fatalf("unexpected success lines:\n%s", out)
		}
		if _, err := os.Stat(marker); err == nil {
			t.Fatal("gpg was invoked on a minisign-only release")
		}
	})

	cases := []struct {
		name  string
		setup func(t *testing.T, dir string)
		want  string
	}{
		{"1 SHA256SUMS tampered under minisign", func(t *testing.T, dir string) {
			removeFile(t, dir, "SHA256SUMS.asc")
			removeFile(t, dir, "SHA512SUMS.asc")
			appendFile(t, dir, "SHA256SUMS", "ffff  evil\n")
		}, "minisign: signature verification failed for SHA256SUMS"},
		{"2 SHA512SUMS tampered under minisign", func(t *testing.T, dir string) {
			removeFile(t, dir, "SHA256SUMS.asc")
			removeFile(t, dir, "SHA512SUMS.asc")
			appendFile(t, dir, "SHA512SUMS", "ffff  evil\n")
		}, "minisign: signature verification failed for SHA512SUMS"},
		{"3a SHA256SUMS tampered under PGP", func(t *testing.T, dir string) {
			removeFile(t, dir, "SHA256SUMS.minisig")
			removeFile(t, dir, "SHA512SUMS.minisig")
			appendFile(t, dir, "SHA256SUMS", "ffff  evil\n")
		}, "PGP: signature verification failed for SHA256SUMS"},
		{"3b SHA512SUMS tampered under PGP", func(t *testing.T, dir string) {
			removeFile(t, dir, "SHA256SUMS.minisig")
			removeFile(t, dir, "SHA512SUMS.minisig")
			appendFile(t, dir, "SHA512SUMS", "ffff  evil\n")
		}, "PGP: signature verification failed for SHA512SUMS"},
		{"4a corrupted minisig", func(t *testing.T, dir string) {
			writeFile(t, dir, "SHA256SUMS.minisig", "untrusted comment: junk\nnot-a-signature\n")
		}, "minisign: signature verification failed for SHA256SUMS"},
		{"4b corrupted asc", func(t *testing.T, dir string) {
			writeFile(t, dir, "SHA256SUMS.asc", "-----BEGIN PGP SIGNATURE-----\n\njunk\n-----END PGP SIGNATURE-----\n")
		}, "PGP: signature verification failed for SHA256SUMS"},
		{"5 minisign signature by wrong key", func(t *testing.T, dir string) {
			f.minisignSign(t, f.wrongSec, dir, "SHA256SUMS")
		}, "minisign: signature verification failed for SHA256SUMS"},
		{"6 PGP signature by foreign signer", func(t *testing.T, dir string) {
			f.pgpSign(t, f.other, dir, "SHA256SUMS")
		}, "PGP: signature verification failed for SHA256SUMS"},
		{"7a exported key has two primaries", func(t *testing.T, dir string) {
			writeFile(t, dir, fixtureName+"-release-signing-key.asc", gpgExport(t, f.gpgHome, f.signer, f.other))
		}, "must contain exactly one primary key"},
		{"7b exported key is empty", func(t *testing.T, dir string) {
			writeFile(t, dir, fixtureName+"-release-signing-key.asc", "")
		}, "PGP: cannot import"},
		{"8a SHA512SUMS.minisig missing", func(t *testing.T, dir string) {
			removeFile(t, dir, "SHA512SUMS.minisig")
		}, "minisign: SHA512SUMS.minisig is missing but SHA512SUMS is present"},
		{"8b SHA512SUMS.asc missing", func(t *testing.T, dir string) {
			removeFile(t, dir, "SHA512SUMS.asc")
		}, "PGP: SHA512SUMS.asc is missing but SHA512SUMS is present"},
		{"9 only SHA512SUMS.minisig present", func(t *testing.T, dir string) {
			removeFile(t, dir, "SHA256SUMS.minisig")
		}, "minisign: SHA256SUMS.minisig is missing but SHA256SUMS is present"},
		{"10 PGP bad while minisign good", func(t *testing.T, dir string) {
			f.pgpSign(t, f.other, dir, "SHA512SUMS")
		}, "PGP: signature verification failed for SHA512SUMS"},
		{"11a minisign public key missing", func(t *testing.T, dir string) {
			removeFile(t, dir, fixtureName+"-minisign.pub")
		}, "minisign: public key " + fixtureName + "-minisign.pub not found"},
		{"11b PGP public key missing", func(t *testing.T, dir string) {
			removeFile(t, dir, fixtureName+"-release-signing-key.asc")
		}, "PGP: public key " + fixtureName + "-release-signing-key.asc not found"},
		{"12 no signature files", func(t *testing.T, dir string) {
			for _, s := range []string{"SHA256SUMS.minisig", "SHA512SUMS.minisig", "SHA256SUMS.asc", "SHA512SUMS.asc"} {
				removeFile(t, dir, s)
			}
		}, "no signatures found to verify"},
		{"13 SHA256SUMS missing", func(t *testing.T, dir string) {
			removeFile(t, dir, "SHA256SUMS")
		}, "SHA256SUMS not found"},
		{"14a orphan SHA512SUMS.minisig", func(t *testing.T, dir string) {
			removeFile(t, dir, "SHA512SUMS")
		}, "minisign: SHA512SUMS.minisig is present but SHA512SUMS is missing"},
		{"14b orphan SHA512SUMS.asc", func(t *testing.T, dir string) {
			removeFile(t, dir, "SHA512SUMS")
			removeFile(t, dir, "SHA256SUMS.minisig")
			removeFile(t, dir, "SHA512SUMS.minisig")
		}, "PGP: SHA512SUMS.asc is present but SHA512SUMS is missing"},
		{"15 signature by expired key", func(t *testing.T, dir string) {
			removeFile(t, dir, "SHA256SUMS.minisig")
			removeFile(t, dir, "SHA512SUMS.minisig")
			for _, m := range []string{"SHA256SUMS", "SHA512SUMS"} {
				f.pgpSign(t, f.expired, dir, m, "--faked-system-time", "20200101T120000!")
			}
			writeFile(t, dir, fixtureName+"-release-signing-key.asc", gpgExport(t, f.gpgHome, f.expired))
		}, "rejected=EXPKEYSIG"},
		{"16 signature by revoked key", func(t *testing.T, dir string) {
			removeFile(t, dir, "SHA256SUMS.minisig")
			removeFile(t, dir, "SHA512SUMS.minisig")
			for _, m := range []string{"SHA256SUMS", "SHA512SUMS"} {
				f.pgpSign(t, f.revoked, dir, m)
			}
			writeFile(t, dir, fixtureName+"-release-signing-key.asc", f.revokedExportWith(t, revCert))
		}, "rejected=REVKEYSIG"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := f.releaseDir(t)
			tc.setup(t, dir)
			out, err := verifyRun(t, dir, "")
			expectVerifyFail(t, out, err, tc.want)
		})
	}
}

// revokedExportWith returns an armored export of the revoked key with its
// revocation applied, built in a scratch homedir so the shared fixture
// keyring keeps an unrevoked copy for signing.
func (f *signingFixture) revokedExportWith(t *testing.T, revCert []byte) string {
	t.Helper()
	if f.revokedExport != "" {
		return f.revokedExport
	}
	home, err := os.MkdirTemp("/tmp", "vsig-rev-")
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
	pub := filepath.Join(home, "revoked.asc")
	writeFile(t, home, "revoked.asc", gpgExport(t, f.gpgHome, f.revoked))
	// gpg prefixes the stored certificate's armor line with ":" to prevent
	// accidental import; strip it to apply the revocation.
	cert := strings.Replace(string(revCert), ":-----BEGIN PGP PUBLIC KEY BLOCK-----", "-----BEGIN PGP PUBLIC KEY BLOCK-----", 1)
	writeFile(t, home, "revoke.asc", cert)
	gpgFixture(t, home, "--import", pub)
	gpgFixture(t, home, "--import", filepath.Join(home, "revoke.asc"))
	f.revokedExport = gpgExport(t, home, f.revoked)
	return f.revokedExport
}
