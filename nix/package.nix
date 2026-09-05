{
  alsa-lib,
  alsa-plugins,
  buildGoModule,
  ffmpeg-headless,
  flac,
  lib,
  libogg,
  libvorbis,
  makeWrapper,
  mpg123,
  pipewire,
  pkg-config,
  symlinkJoin,
  version ? "dev",
  yt-dlp,
}:

let
  # The audio backend talks to ALSA, and on PipeWire/PulseAudio systems the
  # host's /etc/alsa/conf.d routes the default device through a plugin named
  # by bare filename ("libasound_module_pcm_pipewire.so"). Nix's libasound only
  # searches its own store path for plugins, so on non-NixOS hosts that lookup
  # fails and cliamp plays into silence. Ship both plugins and point libasound
  # at them.
  alsaPluginDir = symlinkJoin {
    name = "cliamp-alsa-plugins";
    paths = [
      "${alsa-plugins}/lib/alsa-lib"
      "${pipewire}/lib/alsa-lib"
    ];
  };
in

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
    alsa-lib
    flac
    libogg
    libvorbis
    mpg123
  ];

  ldflags = [
    "-s"
    "-w"
    "-X=main.version=${version}"
  ];

  postInstall = ''
    wrapProgram "$out/bin/cliamp" \
      --prefix PATH : ${lib.makeBinPath [
        ffmpeg-headless
        yt-dlp
      ]} \
      --set-default ALSA_PLUGIN_DIR ${alsaPluginDir}
    install -Dm644 Cliamp.png "$out/share/icons/hicolor/512x512/apps/cliamp.png"
    install -Dm644 cliamp.desktop "$out/share/applications/cliamp.desktop"
  '';

  meta = {
    description = "Retro terminal music player inspired by Winamp";
    homepage = "https://github.com/bjarneo/cliamp";
    license = lib.licenses.mit;
    mainProgram = "cliamp";
    platforms = lib.platforms.linux;
  };
}
