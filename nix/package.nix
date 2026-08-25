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
  version ? "dev",
  yt-dlp,
}:

buildGoModule {
  pname = "cliamp";
  inherit version;

  src = lib.cleanSource ../.;
  vendorHash = "sha256-/c2MOMnG8twpr2/9plFanXkJwoIYNwC0mPksTklIcRw=";

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
      ]}
  '';

  meta = {
    description = "Retro terminal music player inspired by Winamp";
    homepage = "https://github.com/bjarneo/cliamp";
    license = lib.licenses.mit;
    mainProgram = "cliamp";
    platforms = lib.platforms.linux;
  };
}
