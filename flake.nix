{
  description = "A retro terminal music player inspired by Winamp";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs =
    { self, nixpkgs }:
    let
      # x86_64-darwin is intentionally absent: nixpkgs 26.11 (nixos-unstable)
      # dropped support for it. Intel Macs can still use nix/package.nix via a
      # nixpkgs-26.05-darwin channel with callPackage.
      systems = [
        "aarch64-darwin"
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
            buildInputs =
              with pkgs;
              [
                go
                pkg-config
                flac
                libogg
                libvorbis
                mpg123
                ffmpeg-headless
                yt-dlp
              ]
              ++ lib.optionals stdenv.hostPlatform.isLinux [
                alsa-lib
              ];
            shellHook = ''
              echo "🎵 cliamp dev shell loaded"
            '';
          };
        }
      );
    };
}
