{
  alsa-lib,
  buildGoModule,
  ffmpeg-headless,
  flac,
  lib,
  libogg,
  libvorbis,
  makeWrapper,
  mpg123,
  pkg-config,
  stdenv,
  version ? "dev",
  yt-dlp,
}:

buildGoModule {
  pname = "cliamp";
  inherit version;

  src = lib.cleanSource ../.;
  vendorHash = "sha256-rtwUWbft5XGEbuBCn0OMCn4TS5Ul+UXJNIqNOzXfU+M=";

  nativeBuildInputs = [
    makeWrapper
    pkg-config
  ];

  buildInputs = [
    flac
    libogg
    libvorbis
    mpg123
  ]
  ++ lib.optionals stdenv.hostPlatform.isLinux [
    alsa-lib
  ];
  # On darwin the CoreAudio/MediaPlayer/AppKit frameworks referenced by the
  # cgo files come from the Apple SDK that stdenv provides by default.

  ldflags = [
    "-s"
    "-w"
    "-X=main.version=${version}"
  ];

  # Many provider tests stand up an httptest server on loopback. The darwin
  # build sandbox denies that by default, so the check phase dies with
  # "bind: operation not permitted". This is the standard nixpkgs opt-in for
  # tests that need loopback; it has no effect on Linux.
  __darwinAllowLocalNetworking = true;

  postInstall = ''
    wrapProgram "$out/bin/cliamp" \
      --prefix PATH : ${lib.makeBinPath [
        ffmpeg-headless
        yt-dlp
      ]}
  ''
  + lib.optionalString stdenv.hostPlatform.isLinux ''
    install -Dm644 Cliamp.png "$out/share/icons/hicolor/512x512/apps/cliamp.png"
    install -Dm644 cliamp.desktop "$out/share/applications/cliamp.desktop"
  '';

  meta = {
    description = "Retro terminal music player inspired by Winamp";
    homepage = "https://github.com/bjarneo/cliamp";
    license = lib.licenses.mit;
    mainProgram = "cliamp";
    platforms = lib.platforms.linux ++ lib.platforms.darwin;
  };
}
