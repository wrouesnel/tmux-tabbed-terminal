# Packages a binary already built on the target release by `go run mage.go rpm`, which
# passes the version, release and staged files as macros. rpmbuild works out the library
# dependencies (GTK3, VTE) from the binary.

# Go binaries are stripped at build time and carry no build ID.
%global debug_package %{nil}
%global _missing_build_ids_terminate_build 0
%global _build_id_links none

Name:           tmux-tabbed-terminal
Version:        %{pkg_version}
Release:        %{pkg_release}%{?dist}
Summary:        GNOME Terminal style terminal for switching between tmux sessions
# The source is MIT. The binary links github.com/yuseferi/zax, which is GPL-3.0.
License:        MIT AND GPL-3.0-only
URL:            https://github.com/wrouesnel/tmux-tabbed-terminal
Requires:       tmux >= 2.7
Requires:       hicolor-icon-theme

%description
A terminal which looks like GNOME Terminal and lists the tmux sessions running for
the user, on this machine and on other hosts over ssh, in a sidebar. Clicking a
session switches to it, and the window can be split to show several sessions side
by side. Sessions producing output show a pulsing dot, and sessions with output you
haven't seen are marked.

%install
cp -a %{stage}/. %{buildroot}/

%files
%license /usr/share/licenses/tmux-tabbed-terminal/LICENSE
%doc /usr/share/doc/tmux-tabbed-terminal/README.md
%doc /usr/share/doc/tmux-tabbed-terminal/config.example.yml
/usr/bin/tmux-tabbed-terminal
/usr/share/applications/io.github.wrouesnel.TmuxTabbedTerminal.desktop
/usr/share/icons/hicolor/scalable/apps/io.github.wrouesnel.TmuxTabbedTerminal.svg
/usr/share/metainfo/io.github.wrouesnel.TmuxTabbedTerminal.metainfo.xml
