{
  description = "Terminal Deployment Environment";

  inputs = {
    # nixpkgs.url = "github:nixos/nixpkgs";
    flake-utils.url = "github:numtide/flake-utils";
    unstable.url = "github:nixos/nixpkgs/nixpkgs-unstable";
  };

  outputs = { self, flake-utils, unstable }:
    flake-utils.lib.eachDefaultSystem (system:
      let
        pkgs = import unstable { inherit system; };
      in
      {
        devShell = pkgs.mkShell {
          packages = with pkgs; [
            nodejs-16_x
            nodePackages.pnpm
            sops

            go_1_19

            go-outline
            go-tools
            gocode
            gocode-gomod
            godef
            golangci-lint
            golint
            gomodifytags
            gopkgs
            gopls
            gotests
            gotools

            impl
            delve

            go-task

            buf

            wire

            natscli
          ];
        };
      }
    );
}
