#!/usr/bin/env bash
# Build static x86_64 codec libraries for the darwin/amd64 release on an Apple
# Silicon runner. Homebrew no longer installs on Intel macOS, so the x86_64
# libraries come from pinned source releases. Static archives also mean the
# Intel binary needs no Homebrew libraries at run time.
#
# Usage: build-darwin-amd64-codecs.sh <prefix>
# The pkg-config files land in <prefix>/lib/pkgconfig.
set -euo pipefail

PREFIX="${1:?usage: $0 <prefix>}"
WORK="${RUNNER_TEMP:-/tmp}/darwin-amd64-codecs"
mkdir -p "$PREFIX" "$WORK"

export MACOSX_DEPLOYMENT_TARGET=11.0
export CC="clang -arch x86_64"
export CFLAGS="-arch x86_64 -O2 -mmacosx-version-min=${MACOSX_DEPLOYMENT_TARGET}"
export PKG_CONFIG_PATH="$PREFIX/lib/pkgconfig"
CONFIGURE=(--host=x86_64-apple-darwin --prefix="$PREFIX" --disable-shared --enable-static)

# fetch downloads a source archive, checks its SHA-256 sum and unpacks it.
fetch() {
	local url=$1 sum=$2
	local file="$WORK/${url##*/}"
	curl -fsSL "$url" -o "$file"
	echo "$sum  $file" | shasum -a 256 -c -
	tar -xf "$file" -C "$WORK"
}

fetch https://downloads.xiph.org/releases/ogg/libogg-1.3.5.tar.xz \
	c4d91be36fc8e54deae7575241e03f4211eb102afb3fc0775fbbc1b740016705
(
	cd "$WORK/libogg-1.3.5"
	./configure "${CONFIGURE[@]}"
	make -j4
	make install
)

fetch https://downloads.xiph.org/releases/vorbis/libvorbis-1.3.7.tar.xz \
	b33cc4934322bcbf6efcbacf49e3ca01aadbea4114ec9589d1b1e9d20f72954b
(
	cd "$WORK/libvorbis-1.3.7"
	# The 1.3.7 configure script adds -force_cpusubtype_ALL on Darwin.
	# Current Apple toolchains reject that flag.
	perl -pi -e 's/-force_cpusubtype_ALL//g' configure
	./configure "${CONFIGURE[@]}" --disable-docs --disable-examples --disable-oggtest
	make -j4
	make install
)

fetch https://downloads.xiph.org/releases/flac/flac-1.5.0.tar.xz \
	f2c1c76592a82ffff8413ba3c4a1299b6c7ab06c734dee03fd88630485c2b920
(
	cd "$WORK/flac-1.5.0"
	./configure "${CONFIGURE[@]}" --disable-ogg --disable-programs --disable-examples \
		--disable-cpplibs --disable-doxygen-docs
	make -j4
	make install
)

fetch https://www.mpg123.de/download/mpg123-1.32.6.tar.bz2 \
	ccdd1d0abc31d73d8b435fc658c79049d0a905b30669b6a42a03ad169dc609e6
(
	cd "$WORK/mpg123-1.32.6"
	./configure "${CONFIGURE[@]}" --with-cpu=generic_fpu --disable-components \
		--enable-libmpg123 --disable-network
	make -j4
	make install
)

# Every archive must be x86_64, or the Go link fails with a less clear error.
for lib in "$PREFIX"/lib/*.a; do
	lipo -info "$lib"
	lipo "$lib" -verify_arch x86_64
done
