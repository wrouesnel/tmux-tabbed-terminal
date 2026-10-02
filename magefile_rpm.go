//go:build mage

// RPM package, Debian source package (for the PPA) and RPM repository targets.

//nolint:forbidigo,wrapcheck,gosec,mnd
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/pkg/errors"
)

const (
	rpmSpec = "packaging/rpm/tmux-tabbed-terminal.spec"
	// rpmRelease is the package release, before the distribution tag (.el8).
	rpmRelease = "1"
	// rpmRepoSubdir is where the RPM repositories go within the Pages site.
	rpmRepoSubdir = "rpm"
)

// rpmArch maps GOARCH to RPM architecture names.
var rpmArch = map[string]string{
	"amd64": "x86_64",
	"arm64": "aarch64",
}

// rpmVersion is the RPM Version of the build: as the Debian version, which only uses
// characters RPM allows.
func rpmVersion() string {
	return debVersion(version)
}

// Rpm builds an RPM of the platform's binaries into release/. It runs on the release the
// RPM is for (in CI, an EL8 or EL10 container), so the binary links that release's GTK.
//
//nolint:gocritic
func Rpm(OSArch string) error {
	platform, ok := platformsLookup[OSArch]
	if !ok {
		return errors.Wrapf(errPlatformNotSupported, "Rpm: %s", OSArch)
	}
	if err := ReleaseBin(OSArch); err != nil {
		return err
	}

	arch := rpmArch[platform.Arch]
	top := path.Join(binDir, "rpmbuild_"+arch)
	stage := path.Join(top, "stage")
	if err := os.RemoveAll(top); err != nil {
		return err
	}
	if _, _, err := stageInstall(platform, stage); err != nil {
		return err
	}
	if err := copyFile(path.Join(curDir, "LICENSE"),
		path.Join(stage, "usr", "share", "licenses", debPackage, "LICENSE"), 0o644); err != nil {
		return err
	}

	cmd := exec.Command("rpmbuild", "-bb",
		"--define", "_topdir "+top,
		"--define", "pkg_version "+rpmVersion(),
		"--define", "pkg_release "+rpmRelease,
		"--define", "stage "+stage,
		"--target", arch,
		path.Join(curDir, rpmSpec))
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if err := cmd.Run(); err != nil {
		return errors.Wrap(err, "rpmbuild")
	}

	built, err := filepath.Glob(path.Join(top, "RPMS", arch, "*.rpm"))
	if err != nil || len(built) == 0 {
		return errors.New("rpmbuild made no package")
	}
	if err := os.MkdirAll(releaseDir, 0o755); err != nil {
		return err
	}
	for _, rpm := range built {
		dst := path.Join(releaseDir, filepath.Base(rpm))
		fmt.Println("Packaged", dst)
		if err := copyFile(rpm, dst, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// DebSource builds a Debian source package for an Ubuntu suite, such as resolute, into
// release/ppa/, ready to sign and upload to a PPA. Go modules are vendored into it, since
// Launchpad builds without network access. DEB_SIGNING_KEY, if set, signs it with
// dpkg-buildpackage; otherwise it's unsigned.
func DebSource(suite string) error {
	ver := debVersion(version) + "~" + suite + "1"
	work := path.Join(releaseDir, "ppa")
	src := path.Join(work, fmt.Sprintf("%s-%s", debPackage, ver))
	if err := os.RemoveAll(work); err != nil {
		return err
	}
	if err := os.MkdirAll(src, 0o755); err != nil {
		return err
	}

	// The committed tree, so local changes and build output stay out.
	archive := exec.Command("sh", "-c", "git archive --format=tar HEAD | tar -x -C "+shellQuote(src))
	archive.Stderr = os.Stderr
	if err := archive.Run(); err != nil {
		return errors.Wrap(err, "git archive")
	}
	vendor := exec.Command("go", "mod", "vendor")
	vendor.Dir = src
	vendor.Stdout, vendor.Stderr = os.Stdout, os.Stderr
	if err := vendor.Run(); err != nil {
		return errors.Wrap(err, "go mod vendor")
	}

	// debian/ comes from packaging/debian, with a changelog for this version and suite.
	debianDir := path.Join(src, "debian")
	if err := os.Rename(path.Join(src, packagingDir, "debian"), debianDir); err != nil {
		return err
	}
	changelog := fmt.Sprintf("%s (%s) %s; urgency=medium\n\n  * Release %s.\n\n -- %s  %s\n",
		debPackage, ver, suite, version, debMaintainer, sourceDateEpoch().Format("Mon, 02 Jan 2006 15:04:05 -0700"))
	if err := os.WriteFile(path.Join(debianDir, "changelog"), []byte(changelog), 0o644); err != nil {
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

func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// rpmTargets are the EL releases RPMs are built for, by the dist tag in their file names.
var rpmTargets = []string{"el8", "el10"}

// RpmRepo adds dnf repositories to the Pages site in .apt-repo/rpm, one per EL release
// and architecture, from the RPMs in release/ and $RPM_POOL_DIR. With RPM_SIGNING_KEY
// (a key fingerprint) set, the packages and repository metadata are signed with it; the
// key's passphrase must be cached in gpg-agent, as the release workflow does.
func RpmRepo() error {
	repoURL := strings.TrimSuffix(envOr("APT_REPO_URL", aptDefaultURL), "/")
	signingKey := os.Getenv("RPM_SIGNING_KEY")
	site := normalizePath(envOr("APT_REPO_DIR", path.Join(curDir, aptRepoDir)))
	out := path.Join(site, rpmRepoSubdir)
	if err := os.RemoveAll(out); err != nil {
		return err
	}

	var rpms []string
	for _, dir := range []string{releaseDir, os.Getenv("RPM_POOL_DIR")} {
		if dir == "" {
			continue
		}
		matches, err := filepath.Glob(path.Join(dir, "*.rpm"))
		if err != nil {
			return err
		}
		rpms = append(rpms, matches...)
	}
	if len(rpms) == 0 {
		return errors.New("no .rpm packages to publish")
	}

	repos := map[string]bool{}
	for _, rpm := range rpms {
		name := filepath.Base(rpm)
		dist, arch := "", ""
		for _, d := range rpmTargets {
			if strings.Contains(name, "."+d+".") {
				dist = d
			}
		}
		for _, a := range rpmArch {
			if strings.HasSuffix(name, "."+a+".rpm") {
				arch = a
			}
		}
		if dist == "" || arch == "" {
			fmt.Println("Skipping", name, ": no EL release or architecture in its name")
			continue
		}
		dir := path.Join(out, dist, arch)
		dst := path.Join(dir, name)
		if _, err := os.Stat(dst); err == nil {
			continue
		}
		if err := copyFile(rpm, dst, 0o644); err != nil {
			return err
		}
		repos[dir] = true
	}

	dirs := make([]string, 0, len(repos))
	for dir := range repos {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	for _, dir := range dirs {
		if signingKey != "" {
			pkgs, _ := filepath.Glob(path.Join(dir, "*.rpm"))
			args := append([]string{"--define", "_gpg_name " + signingKey, "--addsign"}, pkgs...)
			if _, err := runIn(dir, "rpmsign", args...); err != nil {
				return err
			}
		}
		if _, err := runIn(dir, "createrepo_c", "--general-compress-type=gz", "."); err != nil {
			return err
		}
		if signingKey != "" {
			if _, err := runIn(dir, "gpg", "--batch", "--yes", "--local-user", signingKey,
				"--armor", "--detach-sign", "repodata/repomd.xml"); err != nil {
				return err
			}
		}
	}

	// A .repo file per release for users to install.
	for _, dist := range rpmTargets {
		repo := fmt.Sprintf(`[%[1]s]
name=%[1]s (%[2]s)
baseurl=%[3]s/%[4]s/%[2]s/$basearch
enabled=1
gpgcheck=1
repo_gpgcheck=1
gpgkey=%[3]s/key.asc
`, debPackage, dist, repoURL, rpmRepoSubdir)
		if err := os.WriteFile(path.Join(out, debPackage+"-"+dist+".repo"), []byte(repo), 0o644); err != nil {
			return err
		}
	}
	fmt.Println("RPM repositories written to", out, "on", runtime.GOOS)
	return nil
}
