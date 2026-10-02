//go:build mage

// Debian packaging and APT repository targets.

//nolint:forbidigo,wrapcheck,gosec,mnd
package main

import (
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/template"
	"time"

	"github.com/pkg/errors"
)

const (
	debPackage    = "tmux-tabbed-terminal"
	debMaintainer = "Will Rouesnel <wrouesnel@wrouesnel.com>"
	debHomepage   = "https://github.com/wrouesnel/tmux-tabbed-terminal"
	debSection    = "x11"
	// debExtraDepends are run-time dependencies dpkg-shlibdeps can't see.
	debExtraDepends = "tmux (>= 3.0)"
	appID           = "io.github.wrouesnel.TmuxTabbedTerminal"
	packagingDir    = "packaging"

	// aptDefaultSuite is the distribution the packages are built on and published for.
	aptDefaultSuite = "noble"
	aptComponent    = "main"
	aptDefaultURL   = "https://wrouesnel.github.io/tmux-tabbed-terminal"
	aptRepoDir      = ".apt-repo"
)

var errNoPackages = errors.New("no .deb packages to publish")

// debVersion converts a git describe version to a Debian version: v1.2.3-4-gabc-dirty
// becomes 1.2.3+4.gabc.dirty, which sorts after 1.2.3 and before 1.2.4.
func debVersion(v string) string {
	v = strings.TrimPrefix(v, "v")
	parts := strings.Split(v, "-")
	if len(parts) == 1 {
		return parts[0]
	}
	return parts[0] + "+" + strings.Join(parts[1:], ".")
}

// sourceDateEpoch is the time stamped into packages: SOURCE_DATE_EPOCH if set, otherwise
// the time of the current commit, so rebuilding a commit gives the same package.
func sourceDateEpoch() time.Time {
	stamp := os.Getenv("SOURCE_DATE_EPOCH")
	if stamp == "" {
		stamp = gitOutput("log", "-1", "--format=%ct")
	}
	if secs, err := strconv.ParseInt(stamp, 10, 64); err == nil {
		return time.Unix(secs, 0).UTC()
	}
	return time.Now().UTC()
}

// copyFile copies a file, creating the destination's directory.
func copyFile(src, dst string, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// writeGzip writes data gzip-compressed, as Debian wants for documentation.
func writeGzip(dst string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	buf := new(bytes.Buffer)
	gz, _ := gzip.NewWriterLevel(buf, gzip.BestCompression)
	gz.Header.ModTime = sourceDateEpoch()
	if _, err := gz.Write(data); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return os.WriteFile(dst, buf.Bytes(), 0o644)
}

// shlibDepends asks dpkg-shlibdeps for the library packages the binaries need.
func shlibDepends(binaries []string) (string, error) {
	tmp, err := os.MkdirTemp("", "shlibdeps")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(tmp)
	// dpkg-shlibdeps reads the package list from debian/control.
	control := fmt.Sprintf("Source: %s\n\nPackage: %s\nArchitecture: any\n", debPackage, debPackage)
	if err := os.MkdirAll(path.Join(tmp, "debian"), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(path.Join(tmp, "debian", "control"), []byte(control), 0o644); err != nil {
		return "", err
	}

	args := []string{"-O"}
	for _, bin := range binaries {
		args = append(args, "-e"+bin)
	}
	cmd := exec.Command("dpkg-shlibdeps", args...)
	cmd.Dir = tmp
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	if err != nil {
		return "", errors.Wrap(err, "dpkg-shlibdeps")
	}
	for _, line := range strings.Split(string(out), "\n") {
		if deps, ok := strings.CutPrefix(line, "shlibs:Depends="); ok {
			return strings.TrimSpace(deps), nil
		}
	}
	return "", errors.New("dpkg-shlibdeps printed no dependencies")
}

// dirSizeKiB returns the size of the files under dir, for Installed-Size.
func dirSizeKiB(dir string) (int64, error) {
	var total int64
	err := filepath.Walk(dir, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() {
			total += info.Size()
		}
		return nil
	})
	return (total + 1023) / 1024, err
}

var debControlTemplate = template.Must(template.New("control").Parse(`Package: {{.Package}}
Version: {{.Version}}
Architecture: {{.Arch}}
Maintainer: {{.Maintainer}}
Installed-Size: {{.InstalledSize}}
Depends: {{.Depends}}
Section: {{.Section}}
Priority: optional
Homepage: {{.Homepage}}
Description: {{.Summary}}
 A terminal which looks like GNOME Terminal and lists the tmux sessions running
 for the user in a sidebar. Clicking a session switches to it, and the window
 can be split to show several sessions side by side. Sessions producing output
 show a spinner, and sessions with output you haven't seen are marked.
`))

// Deb builds a Debian package of the platform's binaries into release/.
//
//nolint:gocritic
func Deb(OSArch string) error {
	platform, ok := platformsLookup[OSArch]
	if !ok {
		return errors.Wrapf(errPlatformNotSupported, "Deb: %s", OSArch)
	}
	if err := ReleaseBin(OSArch); err != nil {
		return err
	}

	arch := debArch[platform.Arch]
	ver := debVersion(version)
	stage := path.Join(binDir, fmt.Sprintf("deb_%s_%s_%s", debPackage, ver, arch))
	if err := os.RemoveAll(stage); err != nil {
		return err
	}

	binaries := []string{}
	for _, cmd := range goCmds {
		dst := path.Join(stage, "usr", "bin", cmd)
		if err := copyFile(platform.PlatformBin(cmd), dst, 0o755); err != nil {
			return err
		}
		binaries = append(binaries, dst)
	}

	linuxDir := path.Join(curDir, packagingDir, "linux")
	files := map[string]string{
		appID + ".desktop":      "usr/share/applications/" + appID + ".desktop",
		appID + ".svg":          "usr/share/icons/hicolor/scalable/apps/" + appID + ".svg",
		appID + ".metainfo.xml": "usr/share/metainfo/" + appID + ".metainfo.xml",
	}
	for src, dst := range files {
		if err := copyFile(path.Join(linuxDir, src), path.Join(stage, dst), 0o644); err != nil {
			return err
		}
	}
	docDir := path.Join(stage, "usr", "share", "doc", debPackage)
	if err := copyFile(path.Join(curDir, packagingDir, "config.example.yml"), path.Join(docDir, "config.example.yml"),
		0o644); err != nil {
		return err
	}
	if err := copyFile(path.Join(curDir, "README.md"), path.Join(docDir, "README.md"), 0o644); err != nil {
		return err
	}
	changelog := fmt.Sprintf("%s (%s) %s; urgency=medium\n\n  * Release %s.\n\n -- %s  %s\n",
		debPackage, ver, aptDefaultSuite, version, debMaintainer, sourceDateEpoch().Format("Mon, 02 Jan 2006 15:04:05 -0700"))
	if err := writeGzip(path.Join(docDir, "changelog.gz"), []byte(changelog)); err != nil {
		return err
	}

	depends, err := shlibDepends(binaries)
	if err != nil {
		return err
	}
	size, err := dirSizeKiB(stage)
	if err != nil {
		return err
	}

	control := new(bytes.Buffer)
	if err := debControlTemplate.Execute(control, map[string]interface{}{
		"Package":       debPackage,
		"Version":       ver,
		"Arch":          arch,
		"Maintainer":    debMaintainer,
		"InstalledSize": size,
		"Depends":       depends + ", " + debExtraDepends,
		"Section":       debSection,
		"Homepage":      debHomepage,
		"Summary":       "GNOME Terminal style terminal for switching between tmux sessions",
	}); err != nil {
		return err
	}
	if err := os.MkdirAll(path.Join(stage, "DEBIAN"), 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(path.Join(stage, "DEBIAN", "control"), control.Bytes(), 0o644); err != nil {
		return err
	}

	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return err
	}
	debFile := path.Join(releaseDir, fmt.Sprintf("%s_%s_%s.deb", debPackage, ver, arch))
	fmt.Println("Packaging", debFile)
	cmd := exec.Command("dpkg-deb", "--root-owner-group", "-Zxz", "--build", stage, debFile)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return cmd.Run()
}

// collectDebs returns the .deb files in the given directories, by file name. A later
// directory doesn't replace a package an earlier one has.
func collectDebs(dirs ...string) (map[string]string, error) {
	debs := map[string]string{}
	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		matches, err := filepath.Glob(path.Join(dir, "*.deb"))
		if err != nil {
			return nil, err
		}
		for _, m := range matches {
			if _, ok := debs[filepath.Base(m)]; !ok {
				debs[filepath.Base(m)] = m
			}
		}
	}
	return debs, nil
}

// runIn runs a command in a directory and returns its standard output.
func runIn(dir string, name string, args ...string) ([]byte, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Stderr = os.Stderr
	out, err := cmd.Output()
	return out, errors.Wrapf(err, "%s %s", name, strings.Join(args, " "))
}

// envOr returns an environment variable, or def if it's empty.
func envOr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

var aptIndexTemplate = template.Must(template.New("index").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Package}} APT repository</title>
<style>
body { font-family: system-ui, sans-serif; max-width: 52rem; margin: 2rem auto; padding: 0 1rem; line-height: 1.5; }
pre { background: #f4f4f4; padding: 1rem; overflow-x: auto; }
@media (prefers-color-scheme: dark) { body { background: #1e1e1e; color: #ddd; } pre { background: #2b2b2b; } a { color: #8ab4f8; } }
</style>
</head>
<body>
<h1>{{.Package}}</h1>
<p>APT repository for <a href="{{.Homepage}}">{{.Package}}</a>, built for Ubuntu {{.Suite}} ({{.Archs}}).</p>
<h2>Install</h2>
<pre>sudo install -d -m 0755 /etc/apt/keyrings
sudo curl -fsSLo /etc/apt/keyrings/{{.Package}}.gpg {{.URL}}/key.gpg
sudo tee /etc/apt/sources.list.d/{{.Package}}.sources &lt;&lt;EOF
Types: deb
URIs: {{.URL}}
Suites: {{.Suite}}
Components: {{.Component}}
Signed-By: /etc/apt/keyrings/{{.Package}}.gpg
EOF
sudo apt update
sudo apt install {{.Package}}</pre>
<h2>Packages</h2>
<ul>
{{range .Debs}}<li><a href="pool/{{$.Component}}/{{$.PoolPrefix}}/{{$.Package}}/{{.}}">{{.}}</a></li>
{{end}}</ul>
</body>
</html>
`))

// AptRepo builds an APT repository for GitHub Pages in .apt-repo, from the .deb files in
// release/ and in APT_POOL_DIR, such as packages downloaded from earlier releases.
// APT_SIGNING_KEY names the gpg key which signs it; without it the repository is unsigned
// and only usable with [trusted=yes]. APT_SUITE and APT_REPO_URL override the suite and
// public URL.
func AptRepo() error {
	suite := envOr("APT_SUITE", aptDefaultSuite)
	repoURL := strings.TrimSuffix(envOr("APT_REPO_URL", aptDefaultURL), "/")
	signingKey := os.Getenv("APT_SIGNING_KEY")
	out := normalizePath(envOr("APT_REPO_DIR", path.Join(curDir, aptRepoDir)))

	debs, err := collectDebs(releaseDir, os.Getenv("APT_POOL_DIR"))
	if err != nil {
		return err
	}
	if len(debs) == 0 {
		return errNoPackages
	}

	if err := os.RemoveAll(out); err != nil {
		return err
	}
	poolPrefix := debPackage[:1]
	pool := path.Join(out, "pool", aptComponent, poolPrefix, debPackage)
	names := make([]string, 0, len(debs))
	for name, src := range debs {
		if err := copyFile(src, path.Join(pool, name), 0o644); err != nil {
			return err
		}
		names = append(names, name)
	}
	sort.Strings(names)

	archs := make([]string, 0, len(debArch))
	for _, a := range debArch {
		archs = append(archs, a)
	}
	sort.Strings(archs)

	dist := path.Join("dists", suite)
	for _, arch := range archs {
		binDir := path.Join(out, dist, aptComponent, "binary-"+arch)
		if err := os.MkdirAll(binDir, 0o755); err != nil {
			return err
		}
		packages, err := runIn(out, "apt-ftparchive", "--arch", arch, "packages", "pool")
		if err != nil {
			return err
		}
		if err := os.WriteFile(path.Join(binDir, "Packages"), packages, 0o644); err != nil {
			return err
		}
		if err := writeGzip(path.Join(binDir, "Packages.gz"), packages); err != nil {
			return err
		}
	}

	release, err := runIn(out, "apt-ftparchive",
		"-o", "APT::FTPArchive::Release::Origin="+debPackage,
		"-o", "APT::FTPArchive::Release::Label="+debPackage,
		"-o", "APT::FTPArchive::Release::Suite="+suite,
		"-o", "APT::FTPArchive::Release::Codename="+suite,
		"-o", "APT::FTPArchive::Release::Architectures="+strings.Join(archs, " "),
		"-o", "APT::FTPArchive::Release::Components="+aptComponent,
		"-o", "APT::FTPArchive::Release::Description="+debPackage+" packages",
		"release", dist)
	if err != nil {
		return err
	}
	releaseFile := path.Join(out, dist, "Release")
	if err := os.WriteFile(releaseFile, release, 0o644); err != nil {
		return err
	}

	if signingKey == "" {
		fmt.Println("APT_SIGNING_KEY is not set: the repository is unsigned")
	} else {
		// A passphrase, if the key has one, goes in on stdin rather than the command line.
		passphrase := os.Getenv("APT_SIGNING_PASSPHRASE")
		gpg := func(args ...string) error {
			base := []string{"--batch", "--yes", "--local-user", signingKey}
			if passphrase != "" {
				base = append(base, "--pinentry-mode", "loopback", "--passphrase-fd", "0")
			}
			cmd := exec.Command("gpg", append(base, args...)...)
			cmd.Dir = out
			cmd.Stdin = strings.NewReader(passphrase + "\n")
			cmd.Stderr = os.Stderr
			return errors.Wrapf(cmd.Run(), "gpg %s", strings.Join(args, " "))
		}
		if err := gpg("--clearsign", "--output", path.Join(dist, "InRelease"), path.Join(dist, "Release")); err != nil {
			return err
		}
		if err := gpg("--armor", "--detach-sign", "--output", path.Join(dist, "Release.gpg"),
			path.Join(dist, "Release")); err != nil {
			return err
		}
		key, err := runIn(out, "gpg", "--batch", "--export", signingKey)
		if err != nil {
			return err
		}
		if len(key) == 0 {
			return errors.Errorf("gpg exported no public key for %s", signingKey)
		}
		if err := os.WriteFile(path.Join(out, "key.gpg"), key, 0o644); err != nil {
			return err
		}
		armored, err := runIn(out, "gpg", "--batch", "--armor", "--export", signingKey)
		if err != nil {
			return err
		}
		if err := os.WriteFile(path.Join(out, "key.asc"), armored, 0o644); err != nil {
			return err
		}
	}

	index := new(bytes.Buffer)
	if err := aptIndexTemplate.Execute(index, map[string]interface{}{
		"Package":    debPackage,
		"Homepage":   debHomepage,
		"URL":        repoURL,
		"Suite":      suite,
		"Component":  aptComponent,
		"Archs":      strings.Join(archs, ", "),
		"PoolPrefix": poolPrefix,
		"Debs":       names,
	}); err != nil {
		return err
	}
	if err := os.WriteFile(path.Join(out, "index.html"), index.Bytes(), 0o644); err != nil {
		return err
	}
	// GitHub Pages would otherwise run Jekyll over the repository.
	if err := os.WriteFile(path.Join(out, ".nojekyll"), nil, 0o644); err != nil {
		return err
	}
	fmt.Println("APT repository written to", out)
	return nil
}
