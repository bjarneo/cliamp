{
  description = "A retro terminal music player inspired by Winamp";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      systems = [
        "aarch64-linux"
        "x86_64-linux"
      ];
      forAllSystems = nixpkgs.lib.genAttrs systems;
      version =
        if self ? shortRev then
          self.shortRev
        else if self ? dirtyShortRev then
          self.dirtyShortRev
        else
          "dev";
    in
    {
      packages = forAllSystems (
        system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
          cliamp = pkgs.callPackage ./nix/package.nix { inherit version; };
        in
        {
          inherit cliamp;
          default = cliamp;
        }
      );

      apps = forAllSystems (system: {
        default = {
          type = "app";
          program = nixpkgs.lib.getExe self.packages.${system}.default;
          meta.description = "Run cliamp";
        };
      });

      devShells = forAllSystems (system:
        let
          pkgs = nixpkgs.legacyPackages.${system};
        in
        {
          default = pkgs.mkShell {
            name = "cliamp-dev";
            buildInputs = with pkgs; [
              go
              pkg-config
              alsa-lib
              flac
              libogg
              libvorbis
              mpg123
              libopenmpt
              ffmpeg-headless
              yt-dlp
            ];
            shellHook = ''
              # libopenmpt (tracker module playback) is loaded at runtime via
              # purego/dlopen rather than linked at build time, so - unlike
              # alsa-lib/flac/libvorbis/mpg123, which stdenv links with an
              # rpath baked into the binary - it needs LD_LIBRARY_PATH here
              # too, mirroring nix/package.nix's wrapProgram for the same
              # reason.
              export LD_LIBRARY_PATH="${pkgs.libopenmpt}/lib:$LD_LIBRARY_PATH"
              echo "🎵 cliamp dev shell loaded"
            '';
          };
        }
      );
    };
}
