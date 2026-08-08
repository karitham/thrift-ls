{
  description = "thrift-ls: a Thrift language server and formatter";
  inputs = {
    nixpkgs.url = "github:nixos/nixpkgs";
  };
  outputs =
    { self, nixpkgs }:
    let
      # Evaluate an expression once per supported system (flakeExposed),
      # so every output stays in sync with the same system list.
      forAllSystems = nixpkgs.lib.genAttrs nixpkgs.lib.systems.flakeExposed;
      pkgsFor = system: import nixpkgs { inherit system; };

      # The language server / formatter binary for this module.
      thriftLs =
        pkgs:
        pkgs.buildGoModule {
          pname = "thrift-ls";
          # Keep in sync with the release tag: the binary reports this
          # version and the release workflow injects the same value.
          version = "0.1.1";
          src = nixpkgs.lib.cleanSource ./.;
          vendorHash = "sha256-zWy0x3yktLA8dtcbwzue3aB7a+SlqwWO86G3ZP8DgOQ=";
          ldflags = [
            "-s"
            "-w"
            "-X github.com/karitham/thrift-ls/lsp.ServerVersion=0.1.1"
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
                nodejs_22
              ]
              ++ formatterTools pkgs;
          };
        }
      );
    };
}
