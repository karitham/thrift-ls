{
  description = "thrift-ls: a Thrift language server and formatter";
  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs/nixpkgs-unstable";
  };
  outputs =
    { self, nixpkgs }:
    let
      forAllSystems = nixpkgs.lib.genAttrs nixpkgs.lib.systems.flakeExposed;
      pkgsFor = system: import nixpkgs { inherit system; };

      # Single source of truth for the version, shared with the VS Code
      # extension and the JetBrains plugin.
      version = nixpkgs.lib.removeSuffix "\n" (builtins.readFile ./VERSION);

      thriftLs =
        pkgs:
        pkgs.buildGoModule {
          pname = "thrift-ls";
          inherit version;
          src = nixpkgs.lib.cleanSource ./.;
          vendorHash = "sha256-3SuXwQ0SB7fvsAIPwPTaFzLxAOjPMYwCmBZFun+c1ic=";
          ldflags = [
            "-s"
            "-w"
            "-X github.com/karitham/thrift-ls/lsp.ServerVersion=${version}"
          ];
          meta = {
            description = "A Thrift language server and formatter";
            homepage = "https://github.com/karitham/thrift-ls";
            license = nixpkgs.lib.licenses.asl20;
            mainProgram = "thrift-ls";
          };
        };

      formatterTools =
        pkgs: with pkgs; [
          gofumpt
          nixfmt
        ];
    in
    {
      formatter = forAllSystems (system: (pkgsFor system).treefmt);

      packages = forAllSystems (
        system:
        let
          pkg = thriftLs (pkgsFor system);
        in
        {
          "thrift-ls" = pkg;
          default = pkg;
        }
      );

      devShells = forAllSystems (
        system:
        let
          pkgs = pkgsFor system;
        in
        {
          default = pkgs.mkShell {
            packages =
              with pkgs;
              [
                go
                treefmt
                golangci-lint
                nodejs
              ]
              ++ formatterTools pkgs;
          };
        }
      );
    };
}
