#!/bin/sh
# Prints the gotk3 build tags for the GTK libraries installed, comma separated: none for
# up-to-date libraries, or for example glib_2_56,gtk_3_22,pango_1_42,cairo_1_15 on RHEL 8.
#
# gotk3 binds the newest GTK functions unless told the libraries are older, with a tag for
# every minor version from its lowest supported, in steps, up to before the newest.
set -eu

# tag MODULE PREFIX LOWEST NEWEST STEP
tag() {
	version=$(pkg-config --modversion "$1" 2>/dev/null) || return 0
	minor=$(echo "$version" | cut -d. -f2)
	[ "$minor" -lt "$4" ] || return 0
	minor=$((minor - (minor - $3) % $5))
	[ "$minor" -ge "$3" ] || minor=$3
	echo "$2_$minor"
}

tags=$(
	tag glib-2.0 glib_2 40 68 2
	tag gtk+-3.0 gtk_3 6 24 2
	tag pango pango_1 36 44 2
	tag cairo cairo_1 9 16 1
)
echo "$tags" | paste -sd, -
