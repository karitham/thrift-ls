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

      thriftLs =
        pkgs:
        let
          version = "0.1.8";
        in
        pkgs.buildGoModule {
          pname = "thrift-ls";
          inherit version;
          src = nixpkgs.lib.cleanSource ./.;
          vendorHash = "sha256-zWy0x3yktLA8dtcbwzue3aB7a+SlqwWO86G3ZP8DgOQ=";
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
