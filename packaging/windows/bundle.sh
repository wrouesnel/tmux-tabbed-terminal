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

# The agents the application runs on remote hosts, so one ssh connection, without the
# connection sharing Windows' ssh lacks, carries everything.
sh "$root/packaging/build-agents.sh" "$out/lib/tmux-tabbed-terminal/agent" "$version"

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

# Fonts, drawn with fontconfig and FreeType, as on GNOME: Ubuntu Sans Mono, GNOME
# Terminal's font on Ubuntu, for terminals. The application uses fontconfig when its
# configuration is here. fc-cache lets the installer find the fonts, so the first start
# doesn't have to.
fonts_url=https://raw.githubusercontent.com/canonical/Ubuntu-Sans-Mono-fonts/v1.100
mkdir -p "$out/etc/fonts" "$out/share/fonts" "$out/licenses"
cp "$root/packaging/windows/fonts.conf" "$out/etc/fonts/"
while read -r sum file dest; do
	curl -fsSL --retry 3 -o "$out/$dest" "$fonts_url/$file"
	echo "$sum  $out/$dest" | sha256sum -c --quiet
done <<'FONTS'
535bee02748d1e396eb22766f6443d450b65b3413b9bd8217cc386d885548103 fonts/ttf/UbuntuSansMono-Regular.ttf share/fonts/UbuntuSansMono-Regular.ttf
e7bc04f57eea16d5dc78ce967ab3fe8075c7f6ab36f5b60f1ba6e94e2c5b13e8 fonts/ttf/UbuntuSansMono-Bold.ttf share/fonts/UbuntuSansMono-Bold.ttf
30600af06cbd461449ad1805b8ef30767ab1ddde79a74cc2261f77be0196084e fonts/ttf/UbuntuSansMono-Italic.ttf share/fonts/UbuntuSansMono-Italic.ttf
91d41504ddca5aa91636078dd9bf547d6e52ffd22fa422eb888329017bddd6c3 fonts/ttf/UbuntuSansMono-BoldItalic.ttf share/fonts/UbuntuSansMono-BoldItalic.ttf
2f0015108d68627bd788d313f529c21ff4da2c2c42a5e1f3883acc83480f9002 LICENCE.txt licenses/ubuntu-sans-mono.txt
FONTS
take "$out/bin" /ucrt64/bin/fc-cache.exe
copy_deps "$out/bin" /ucrt64/ "$out/bin/fc-cache.exe"

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
{
	echo "tmux-tabbed-terminal $version bundles these MSYS2 packages, unmodified."
	echo "Their sources are at https://github.com/msys2/MSYS2-packages (msys) and"
	echo "https://github.com/msys2/MINGW-packages (mingw-w64-ucrt-x86_64), by package name,"
	echo "and the source archives at https://repo.msys2.org/."
	echo
	echo "It also bundles the Ubuntu Sans Mono fonts, v1.100, unmodified, under the Ubuntu"
	echo "Font Licence in ubuntu-sans-mono.txt. They're from"
	echo "https://github.com/canonical/Ubuntu-Sans-Mono-fonts."
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
