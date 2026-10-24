#!/usr/bin/env bash
# Builds the Windows bundle the installer installs: the application and the GTK runtime it
# needs, under bin, lib and share, and beside them, under msys, a private MSYS2 tree with
# tmux and the few programs the terminal runs it with. Nothing needs installing besides.
#
# Run it in an MSYS2 UCRT64 shell with the packages the Windows CI job installs:
#
#   packaging/windows/bundle.sh OUTDIR [VERSION]
set -euo pipefail

out=${1:?usage: bundle.sh OUTDIR [VERSION]}
root=$(cd "$(dirname "$0")/../.." && pwd)
version=${2:-$(git -C "$root" describe --tags --always --dirty)}
module=$(cd "$root" && go list -m)

rm -rf "$out"
mkdir -p "$out/bin"
# sources lists the system files bundled, for their packages' licenses.
sources=$(mktemp)
trap 'rm -f "$sources"' EXIT

# take DEST FILE...: copies the files into DEST, noting where they came from.
take() {
	local dest=$1
	shift
	cp -n "$@" "$dest/"
	printf '%s\n' "$@" >>"$sources"
}

# copy_deps DEST PREFIX FILE...: copies the DLLs the files load from PREFIX, such as
# /ucrt64/, into DEST.
copy_deps() {
	local dest=$1 prefix=$2
	shift 2
	ldd "$@" | awk -v prefix="$prefix" 'index($3, prefix) == 1 { print $3 }' | sort -u |
		while read -r dll; do
			take "$dest" "$dll"
		done
}

# The application, as a GUI program, which Windows starts without a console.
(cd "$root" && go build -trimpath \
	-ldflags "-s -w -H windowsgui -X $module/version.Version=$version" \
	-o "$out/bin/tmux-tabbed-terminal.exe" ./cmd/tmux-tabbed-terminal)
copy_deps "$out/bin" /ucrt64/ "$out/bin/tmux-tabbed-terminal.exe"

# GTK's image loaders, for icons and image previews. The cache's paths are relative to the
# installation, so the system's, cut down to these loaders, serves.
loaders=lib/gdk-pixbuf-2.0/2.10.0/loaders
mkdir -p "$out/$loaders"
for loader in png jpeg gif bmp ico svg; do
	take "$out/$loaders" /ucrt64/$loaders/*pixbufloader*[-_]$loader.dll
done
copy_deps "$out/bin" /ucrt64/ "$out/$loaders"/*.dll
awk -v keep="$(cd "$out/$loaders" && ls)" '
	BEGIN { RS = ""; ORS = "\n\n"; n = split(keep, names, "\n"); for (i = 1; i <= n; i++) want[names[i]] = 1 }
	/^#/ { print; next }
	{ split($1, path, "\\\\"); name = path[length(path)]; gsub(/"/, "", name); if (name in want) print }
' /ucrt64/lib/gdk-pixbuf-2.0/2.10.0/loaders.cache >"$out/lib/gdk-pixbuf-2.0/2.10.0/loaders.cache"

# The settings GTK reads, such as the file chooser's, and the icon themes.
mkdir -p "$out/share/glib-2.0/schemas" "$out/share/icons/hicolor"
take "$out/share/glib-2.0/schemas" /ucrt64/share/glib-2.0/schemas/org.gtk.Settings.*.gschema.xml
glib-compile-schemas "$out/share/glib-2.0/schemas"
cp -r /ucrt64/share/icons/Adwaita "$out/share/icons/"
echo /ucrt64/share/icons/Adwaita/index.theme >>"$sources"
take "$out/share/icons/hicolor" /ucrt64/share/icons/hicolor/index.theme

# tmux, and what the terminal widget runs it with on a Cygwin pty: script, stty and tty,
# under sh, and cygpath for paths. bash is a shell to choose. Without the console helper,
# the runtime opens a visible console window where it needs a hidden one.
msys=$out/msys
mkdir -p "$msys/usr/bin" "$msys/etc" "$msys/tmp"
programs=(tmux script stty tty sh bash cygpath cygwin-console-helper)
for program in "${programs[@]}"; do
	take "$msys/usr/bin" "/usr/bin/$program.exe"
done
copy_deps "$msys/usr/bin" /usr/bin/ "$msys/usr/bin"/*.exe
for term in xterm-256color tmux-256color tmux screen-256color screen; do
	entry=$(find /usr/share/terminfo -name "$term" | head -n 1)
	mkdir -p "$msys/$(dirname "$entry")"
	take "$msys/$(dirname "$entry")" "$entry"
done
cp /etc/fstab "$msys/etc/"
# tmux names a window after its program, which for a Windows program is its path, such as
# C:\Windows\System32\cmd.exe: name it cmd instead. ~/.tmux.conf is read after this.
cat >"$msys/etc/tmux.conf" <<'EOF'
set -g automatic-rename-format '#{?pane_in_mode,[tmux],#{s/\.[eE][xX][eE]$//:#{s/^.*[\\]//:pane_current_command}}}#{?pane_dead,[dead],}'
EOF
# Home is the Windows profile folder, as for the user's other programs, not one in here.
cat >"$msys/etc/nsswitch.conf" <<'EOF'
passwd: files db
group: files db
db_enum: cache builtin
db_home: windows
db_shell: cygwin desc
db_gecos: cygwin desc
EOF

# The licenses and versions of what's bundled, with where their source is.
mkdir -p "$out/licenses"
{
	echo "tmux-tabbed-terminal $version bundles these MSYS2 packages, unmodified."
	echo "Their sources are at https://github.com/msys2/MSYS2-packages (msys) and"
	echo "https://github.com/msys2/MINGW-packages (mingw-w64-ucrt-x86_64), by package name,"
	echo "and the source archives at https://repo.msys2.org/."
	echo
	sort -u "$sources" | xargs pacman -Qqo | sort -u | while read -r pkg; do
		pacman -Qi "$pkg" | awk -F' *: ' '/^(Name|Version|Licenses|URL) /{ printf "%s: %s\n", $1, $2 }'
		echo
		for dir in /usr/share/licenses/"$pkg" /ucrt64/share/licenses/"${pkg#mingw-w64-ucrt-x86_64-}"; do
			if [ -d "$dir" ]; then
				cp -r "$dir" "$out/licenses/$pkg"
			fi
		done
	done
} >"$out/licenses/PACKAGES.txt"
cp "$root/LICENSE" "$out/licenses/tmux-tabbed-terminal.txt"

du -sh "$out"
