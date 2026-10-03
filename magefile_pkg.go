//go:build mage

// Source release targets: the vendored source tarball, the source RPM for COPR and the
// Debian source package for the PPA. Packages are built from these by COPR and Launchpad.

//nolint:forbidigo,wrapcheck,gosec,mnd
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/pkg/errors"
)

const (
	pkgName       = "tmux-tabbed-terminal"
	pkgMaintainer = "Will Rouesnel <wrouesnel@wrouesnel.com>"
	packagingDir  = "packaging"
	rpmSpecIn     = "packaging/rpm/tmux-tabbed-terminal.spec.in"
	// debGoPackage is the Go build dependency of Ubuntu suites whose default Go is new
	// enough for go.mod.
	debGoPackage = "golang-go (>= 2:1.24~)"
)

// debGoPackages are the Go build dependencies of Ubuntu suites whose default Go is too
// old: there, a newer Go is packaged beside it. Launchpad only uses the first alternative
// of a build dependency, so each suite names its own.
var debGoPackages = map[string]string{
	"noble": "golang-1.24-go",
}

// pkgVersion converts a git describe version to a package version: v1.2.3-4-gabc-dirty
// becomes 1.2.3+4.gabc.dirty, which sorts after 1.2.3 and before 1.2.4 in both Debian and
// RPM, and uses only characters both allow.
func pkgVersion() string {
	v := strings.TrimPrefix(version, "v")
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

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// vendoredTree writes the committed tree into dir with the Go modules vendored, so it
// builds without network access. Local changes and build output stay out.
func vendoredTree(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	archive := exec.Command("sh", "-c", "git archive --format=tar HEAD | tar -x -C "+shellQuote(dir))
	archive.Stderr = os.Stderr
	if err := archive.Run(); err != nil {
		return errors.Wrap(err, "git archive")
	}
	vendor := exec.Command("go", "mod", "vendor")
	vendor.Dir = dir
	vendor.Stdout, vendor.Stderr = os.Stdout, os.Stderr
	return errors.Wrap(vendor.Run(), "go mod vendor")
}

// sourceTarball is the release's vendored source tarball.
func sourceTarball() string {
	return path.Join(releaseDir, "source", fmt.Sprintf("%s-%s.tar.gz", pkgName, pkgVersion()))
}

// Source builds the release's source tarball into release/source/, with the Go modules
// vendored and a top directory of tmux-tabbed-terminal-<version>, and its SHA-256. The
// source RPM is built from it, and it's attached to GitHub releases.
func Source() error {
	tarball := sourceTarball()
	work := path.Join(binDir, "source")
	top := fmt.Sprintf("%s-%s", pkgName, pkgVersion())
	if err := os.RemoveAll(work); err != nil {
		return err
	}
	if err := vendoredTree(path.Join(work, top)); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(tarball), 0o755); err != nil {
		return err
	}
	// Ordered, owned by root and dated to the commit, so the tarball is reproducible.
	tar := exec.Command("tar", "--sort=name", "--owner=0", "--group=0", "--numeric-owner",
		"--mtime=@"+strconv.FormatInt(sourceDateEpoch().Unix(), 10), "-czf", tarball, top)
	tar.Dir = work
	tar.Env = append(os.Environ(), "GZIP=-n")
	tar.Stderr = os.Stderr
	if err := tar.Run(); err != nil {
		return errors.Wrap(err, "tar")
	}

	f, err := os.Open(tarball)
	if err != nil {
		return err
	}
	defer f.Close()
	sum := sha256.New()
	if _, err := io.Copy(sum, f); err != nil {
		return err
	}
	line := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum.Sum(nil)), filepath.Base(tarball))
	if err := os.WriteFile(tarball+".sha256", []byte(line), 0o644); err != nil {
		return err
	}
	fmt.Println("Source tarball written to", tarball)
	return nil
}

// Srpm builds the source RPM for COPR into release/srpm/, from the source tarball and the
// spec in packaging/rpm, with the version filled in.
func Srpm() error {
	if err := Source(); err != nil {
		return err
	}
	out := path.Join(releaseDir, "srpm")
	if err := os.RemoveAll(out); err != nil {
		return err
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	tmpl, err := os.ReadFile(path.Join(curDir, rpmSpecIn))
	if err != nil {
		return err
	}
	spec := strings.NewReplacer(
		"@VERSION@", pkgVersion(),
		"@DATE@", sourceDateEpoch().Format("Mon Jan 02 2006"),
	).Replace(string(tmpl))
	specPath := path.Join(out, pkgName+".spec")
	if err := os.WriteFile(specPath, []byte(spec), 0o644); err != nil {
		return err
	}

	// --nodeps: the source RPM is built here, but its build dependencies are for COPR.
	cmd := exec.Command("rpmbuild", "-bs", "--nodeps",
		"--define", "_sourcedir "+filepath.Dir(sourceTarball()),
		"--define", "_srcrpmdir "+out,
		specPath)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	return errors.Wrap(cmd.Run(), "rpmbuild")
}

// DebSource builds a Debian source package for an Ubuntu suite, such as noble or
// resolute, into release/ppa/<suite>/, ready to sign and upload to a PPA. Go modules are
// vendored into it, since Launchpad builds without network access. DEB_SIGNING_KEY, if set,
// signs it with dpkg-buildpackage; otherwise it's unsigned.
func DebSource(suite string) error {
	ver := pkgVersion() + "~" + suite + "1"
	work := path.Join(releaseDir, "ppa", suite)
	src := path.Join(work, fmt.Sprintf("%s-%s", pkgName, ver))
	if err := os.RemoveAll(work); err != nil {
		return err
	}
	if err := vendoredTree(src); err != nil {
		return err
	}

	// debian/ comes from packaging/debian, with a changelog for this version and suite.
	debianDir := path.Join(src, "debian")
	if err := os.Rename(path.Join(src, packagingDir, "debian"), debianDir); err != nil {
		return err
	}
	changelog := fmt.Sprintf("%s (%s) %s; urgency=medium\n\n  * Release %s.\n\n -- %s  %s\n",
		pkgName, ver, suite, version, pkgMaintainer, sourceDateEpoch().Format("Mon, 02 Jan 2006 15:04:05 -0700"))
	if err := os.WriteFile(path.Join(debianDir, "changelog"), []byte(changelog), 0o644); err != nil {
		return err
	}
	goPackage, ok := debGoPackages[suite]
	if !ok {
		goPackage = debGoPackage
	}
	control, err := os.ReadFile(path.Join(debianDir, "control"))
	if err != nil {
		return err
	}
	control = []byte(strings.ReplaceAll(string(control), "@GO_PACKAGE@", goPackage))
	if err := os.WriteFile(path.Join(debianDir, "control"), control, 0o644); err != nil {
		return err
	}

	args := []string{"-S", "-d", "-sa"}
	if key := os.Getenv("DEB_SIGNING_KEY"); key != "" {
		args = append(args, "-k"+key)
	} else {
		args = append(args, "-us", "-uc")
	}
	build := exec.Command("dpkg-buildpackage", args...)
	build.Dir = src
	build.Stdout, build.Stderr = os.Stdout, os.Stderr
	if err := build.Run(); err != nil {
		return errors.Wrap(err, "dpkg-buildpackage")
	}
	fmt.Println("Source package written to", work)
	return nil
}
