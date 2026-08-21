#Requires -Version 7.0
<#
.SYNOPSIS
    Reproducible Windows cgo build of cliamp using only zig as the C toolchain.

.DESCRIPTION
    Provisions a self-contained toolchain under .cgo-deps/ (vendored MSYS2 dev
    libraries, a minimal pkg-config shim, a source-built libogg with IOVEC
    support, and libwinpthread-1.dll compiled from zig's bundled winpthreads),
    then builds the binary and copies the required runtime DLLs next to it.

.PARAMETER Output
    Path of the built executable. Defaults to <repo>\zig-build\cliamp.exe.
    Runtime DLLs are copied next to it.

.PARAMETER Test
    Run the full test suite (go test ./...) in the same environment after building.

.PARAMETER ProvisionOnly
    Provision .cgo-deps/ without building.

.PARAMETER ForceProvision
    Re-run provisioning even if the recipe marker matches.

.EXAMPLE
    pwsh scripts/build-zig.ps1 -Test
#>
[CmdletBinding()]
param(
    [string]$Output,
    [switch]$Test,
    [switch]$ProvisionOnly,
    [switch]$ForceProvision
)

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$RecipeVersion = '2026-08-21.1'

$Root  = (Resolve-Path (Join-Path $PSScriptRoot '..')).Path
$Deps  = Join-Path $Root '.cgo-deps'
$Mingw = Join-Path $Deps 'mingw64'
if (-not $Output) { $Output = Join-Path $Root 'zig-build/cliamp.exe' }

$RepoUrl = 'https://repo.msys2.org/mingw/mingw64'
$Packages = @(
    @{ File = 'mingw-w64-x86_64-libogg-1.3.6-1-any.pkg.tar.zst';    Check = 'lib/libogg.dll.a' }
    @{ File = 'mingw-w64-x86_64-libvorbis-1.3.7-3-any.pkg.tar.zst'; Check = 'lib/libvorbis.dll.a' }
    @{ File = 'mingw-w64-x86_64-flac-1.5.0-2-any.pkg.tar.zst';      Check = 'bin/libFLAC.dll' }
    @{ File = 'mingw-w64-x86_64-mpg123-1.33.7-1-any.pkg.tar.zst';   Check = 'lib/libmpg123.dll.a' }
)
$OggSourceUrl = 'https://downloads.xiph.org/releases/ogg/libogg-1.3.6.tar.gz'
$OggSourceDir = Join-Path $Deps 'libogg-1.3.6'
$ImportLibs   = 'ogg', 'vorbis', 'vorbisenc', 'FLAC', 'mpg123'
$RuntimeDlls  = 'libogg-0.dll', 'libvorbis-0.dll', 'libvorbisenc-2.dll',
                'libvorbisfile-3.dll', 'libFLAC.dll', 'libmpg123-0.dll', 'libwinpthread-1.dll'

function Assert-Tool {
    param([string]$Name, [string]$Hint)
    if (-not (Get-Command $Name -ErrorAction SilentlyContinue)) {
        throw "Required tool '$Name' not found on PATH. $Hint"
    }
}

function Save-File {
    param([string]$Url, [string]$Dest)
    New-Item -ItemType Directory -Force -Path (Split-Path $Dest) | Out-Null
    Write-Host "downloading $(Split-Path $Url -Leaf)"
    Invoke-WebRequest -UseBasicParsing -Uri $Url -OutFile $Dest
}

$shimSource = @'
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	exe, err := os.Executable()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	root := filepath.Join(filepath.Dir(filepath.Dir(exe)), "mingw64")
	root = filepath.ToSlash(root)

	args := os.Args[1:]
	wantCflags := false
	wantLibs := false
	var names []string
	for _, a := range args {
		switch {
		case a == "--cflags":
			wantCflags = true
		case a == "--libs":
			wantLibs = true
		case strings.HasPrefix(a, "--"):
		default:
			names = append(names, a)
		}
	}

	libs := map[string][]string{
		"ogg":       {"ogg"},
		"vorbis":    {"vorbis", "ogg"},
		"vorbisenc": {"vorbisenc", "vorbis", "ogg"},
		"flac":      {"FLAC", "ogg"},
		"libmpg123": {"mpg123", "shlwapi"},
	}

	switch {
	case wantCflags && wantLibs:
		fmt.Printf("-I%s/include -L%s/lib -logg -lvorbis -lvorbisenc -lFLAC -lmpg123\n", root, root)
	case wantCflags:
		fmt.Printf("-I%s/include\n", root)
	case wantLibs:
		var seen []string
		add := func(l string) {
			for _, s := range seen {
				if s == l {
					return
				}
			}
			seen = append(seen, l)
		}
		for _, n := range names {
			for _, l := range libs[n] {
				add(l)
			}
		}
		out := fmt.Sprintf("-L%s/lib", root)
		for _, l := range seen {
			out += " -l" + l
		}
		fmt.Println(out)
	}
}
'@

$configTypesH = @'
#ifndef OGG_CONFIG_TYPES_H
#define OGG_CONFIG_TYPES_H
#include <stdint.h>
typedef int16_t ogg_int16_t;
typedef uint16_t ogg_uint16_t;
typedef int32_t ogg_int32_t;
typedef uint32_t ogg_uint32_t;
typedef int64_t ogg_int64_t;
typedef uint64_t ogg_uint64_t;
#endif
'@

function Invoke-Provision {
    $marker = Join-Path $Deps '.provisioned'
    if ((Test-Path $marker) -and -not $ForceProvision -and
        (Get-Content $marker -Raw).Trim() -eq $RecipeVersion) {
        return
    }

    Assert-Tool 'zig' 'Install zig: https://ziglang.org/download/'
    Assert-Tool 'tar' 'bsdtar ships with Windows 10+.'
    New-Item -ItemType Directory -Force -Path $Deps, (Join-Path $Deps 'bin') | Out-Null

    foreach ($pkg in $Packages) {
        if (Test-Path (Join-Path $Mingw ($pkg.Check -replace '/', '\'))) { continue }
        $archive = Join-Path $Deps $pkg.File
        if (-not (Test-Path $archive)) { Save-File "$RepoUrl/$($pkg.File)" $archive }
        Write-Host "extracting $($pkg.File)"
        tar -xf $archive -C $Deps
        if ($LASTEXITCODE -ne 0) { throw "tar failed extracting $($pkg.File)" }
    }

    $staticArchives = Get-ChildItem (Join-Path $Mingw 'lib') -Filter '*.a' |
        Where-Object Name -NotLike '*.dll.a'
    $staticArchives | Remove-Item -Force
    foreach ($name in $ImportLibs) {
        $src = Join-Path $Mingw "lib/lib$name.dll.a"
        $dst = Join-Path $Mingw "lib/$name.lib"
        if ((Test-Path $src) -and -not (Test-Path $dst)) {
            Copy-Item $src $dst
        }
    }

    Set-Content -Encoding Ascii -Path (Join-Path $Mingw 'include/ogg/config_types.h') -Value $configTypesH

    $shim = Join-Path $Deps 'pkgconfig.go'
    Set-Content -Encoding Utf8 -Path $shim -Value $shimSource
    $env:CGO_ENABLED = '0'
    go build -o (Join-Path $Deps 'bin/pkg-config.exe') $shim
    if ($LASTEXITCODE -ne 0) { throw 'failed to build pkg-config shim' }

    if (-not (Test-Path $OggSourceDir)) {
        $tarball = Join-Path $Deps 'libogg-1.3.6.tar.gz'
        if (-not (Test-Path $tarball)) { Save-File $OggSourceUrl $tarball }
        tar -xzf $tarball -C $Deps
        if ($LASTEXITCODE -ne 0) { throw 'tar failed extracting libogg source' }
    }
    Write-Host 'compiling libogg from source (IOVEC enabled)'
    $inc = @("-I$($OggSourceDir -replace '\\','/')/include", "-I$($Mingw -replace '\\','/')/include")
    $objs = @()
    foreach ($c in 'framing.c', 'bitwise.c') {
        $obj = Join-Path $Deps ("$([IO.Path]::GetFileNameWithoutExtension($c)).o")
        zig cc -target x86_64-windows-gnu -O2 @inc -c (Join-Path $OggSourceDir "src/$c") -o $obj
        if ($LASTEXITCODE -ne 0) { throw "zig cc failed compiling $c" }
        $objs += $obj
    }
    Remove-Item (Join-Path $Mingw 'lib/ogg.lib') -Force -ErrorAction SilentlyContinue
    zig ar rcs (Join-Path $Mingw 'lib/ogg.lib') @objs
    if ($LASTEXITCODE -ne 0) { throw 'zig ar failed building ogg.lib' }

    $pthreadDll = Join-Path $Mingw 'bin/libwinpthread-1.dll'
    if (-not (Test-Path $pthreadDll)) {
        $zigHome = Split-Path (Get-Command zig).Source -Parent
        $wpSrc = Join-Path $zigHome 'lib/libc/mingw/winpthreads'
        if (-not (Test-Path $wpSrc)) {
            throw "winpthreads sources not found at '$wpSrc'; check your zig installation layout."
        }
        Write-Host 'compiling libwinpthread-1.dll from zig bundled sources'
        $sources = Get-ChildItem $wpSrc -Filter '*.c' | ForEach-Object { $_.FullName }
        zig cc -target x86_64-windows-gnu -shared -O2 -DIN_WINPTHREAD -DDLL_EXPORT `
            -o $pthreadDll @sources
        if ($LASTEXITCODE -ne 0) { throw 'zig cc failed building libwinpthread-1.dll' }
    }

    Set-Content -Encoding Ascii -Path $marker -Value $RecipeVersion
    Write-Host "provisioned $Deps (recipe $RecipeVersion)"
}

Set-Location $Root
Invoke-Provision
if ($ProvisionOnly) { return }

Assert-Tool 'go' 'Install Go: https://go.dev/dl/'

$outDir = Split-Path $Output -Parent
New-Item -ItemType Directory -Force -Path $outDir | Out-Null
foreach ($dll in $RuntimeDlls) {
    Copy-Item (Join-Path $Mingw "bin/$dll") $outDir -Force
}

$env:PATH = "$Deps/bin;$Mingw/bin;$env:PATH"
$env:CGO_ENABLED = '1'
$env:CC = 'zig cc -target x86_64-windows-gnu'

$version = (git describe --tags --always --dirty 2>$null)
if (-not $version) { $version = 'dev' }

Write-Host "building $Output (version $version)"
go build -trimpath -ldflags "-s -w -X main.version=$version" -o $Output .
if ($LASTEXITCODE -ne 0) { throw 'go build failed' }

& $Output --help *> $null
if ($LASTEXITCODE -ne 0) { throw "smoke test failed: $Output did not respond to --help" }
Write-Host "ok: $Output"

if ($Test) {
    go test ./...
    if ($LASTEXITCODE -ne 0) { throw 'go test failed' }
}
