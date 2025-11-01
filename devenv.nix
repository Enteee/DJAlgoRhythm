{
  pkgs,
  lib,
  config,
  inputs,
  ...
}:

let
  nixpkgsWithConfig = nixpkgs: pkgs.callPackage (import nixpkgs) { };

  pkgs-unstable = nixpkgsWithConfig inputs.nixpkgs-unstable;

  # fix locale
  use-locale = "C.UTF-8";
  custom-locales = pkgs.pkgs.glibcLocalesUtf8.override {
    allLocales = false;
    locales = [ "${use-locale}/UTF-8" ];
  };

  # Minimal packages for CI/CD (essential tools only)
  # Go compiler comes from languages.go.enable, not from this list
  minimalPackages = with pkgs; [
    # git
    git
    git-lfs

    # Go development tools
    go-tools
    gotools
    golangci-lint
    gosec
    govulncheck
    air
  ];

in
{
  env = {
    # set do not track
    # see:
    # - https://consoledonottrack.com/
    DO_NOT_TRACK = 1;

    # fix locale
    LOCALE_ARCHIVE = "${custom-locales}/lib/locale/locale-archive";
    LC_ALL = use-locale;
    LC_CTYPE = use-locale;
    LC_ADDRESS = use-locale;
    LC_IDENTIFICATION = use-locale;
    LC_MEASUREMENT = use-locale;
    LC_MESSAGES = use-locale;
    LC_MONETARY = use-locale;
    LC_NAME = use-locale;
    LC_NUMERIC = use-locale;
    LC_PAPER = use-locale;
    LC_TELEPHONE = use-locale;
    LC_TIME = use-locale;
    LC_COLLATE = use-locale;
  };

  # https://devenv.sh/profiles/
  # Define profiles for different environments
  profiles = {
    # Minimal profile - only essential tools for CI/CD
    # Languages (like Go) are configured separately and work in all profiles
    minimal.module = {
      packages = minimalPackages;
    };
  };

  # https://devenv.sh/packages/
  # Default packages (full development environment)
  # Use --profile minimal for CI/CD to get only essential tools
  packages =
    minimalPackages
    ++ (with pkgs; [
      # Github
      gh

      # AI
      claude-code

      # IDE
      (vscode-with-extensions.override {
        vscodeExtensions =
          with vscode-extensions;
          [
            bbenoist.nix
            mkhl.direnv

            davidanson.vscode-markdownlint
            vscode-extensions.golang.go
          ]
          ++ vscode-utils.extensionsFromVscodeMarketplace [
            {
              name = "hadolint";
              publisher = "exiasr";
              version = "1.1.2";
              sha256 = "sha256-6GO1f8SP4CE8yYl87/tm60FdGHqHsJA4c2B6UKVdpgM=";
            }
          ];
      })
    ])
    # Linux-only packages (Telegram depends on Wayland which is Linux-only)
    ++ (lib.optionals pkgs.stdenv.isLinux (
      with pkgs;
      [
        telegram-desktop
      ]
    ));

  # https://devenv.sh/languages/
  languages.nix.enable = true;
  languages.go.enable = true;

  treefmt.config.programs = {
    dos2unix.enable = true;
  };

  # https://devenv.sh/git-hooks/
  git-hooks.hooks = {
    trim-trailing-whitespace.enable = true;

    nixfmt-rfc-style.enable = true;

    shellcheck = {
      enable = true;
      args = [
        "-x"
        "-o"
        "all"
      ];
    };

    hadolint.enable = true;

    markdownlint = {
      enable = true;
      settings.configuration = {
        MD013 = {
          line_length = 120;
          tables = false;
        };
        MD033 = false; # Allow inline HTML
      };
    };

    yamllint = {
      enable = true;
      settings = {
        strict = true;
        configData = ''{ extends: default, rules: { document-start: disable, line-length: {max: 165}, comments-indentation: disable } }'';
      };
    };

    check-json.enable = true;

    check-toml.enable = true;

    trufflehog.enable = true;
    ripsecrets.enable = true;

    typos = {
      enable = true;
      exclude_types = [
        "svg"
      ];
      excludes = [
        "internal/i18n/messages_ch_be.go"
        ".golangci.yml"
        "go.mod"
        "internal/http/web/static/fontawesome/css/"
      ];
    };
  };

  dotenv = {
    enable = false;
    disableHint = true;
  };

  enterShell = ''
    init(){
      (
        set -euo pipefail
        cd '${config.devenv.root}'

        # - pull lfs artifacts
        # Note: we can not install lfs hooks because,
        # hooks are managed by devenv
        git lfs install --skip-repo
        git lfs pull

        # is interactive shell?
        if tty -s; then
          # print help
          devenv-help
        fi
      )
    }
    if ! init; then
      echo "[!] Failed initializing shell!"
      exit 1
    fi
  '';

  scripts.devenv-help = {
    description = "Print this help";
    exec = ''
      set -euo pipefail
      cd '${config.devenv.root}'

      echo
      echo "Helper scripts provided by the devenv:"
      echo
      sed -e 's| |XXXXXX|g' -e 's|=| |' <<EOF | column -t | sed -e 's|^|- |' -e 's|XXXXXX| |g'
      ${lib.generators.toKeyValue { } (lib.mapAttrs (name: value: value.description) config.scripts)}
      EOF
      echo
    '';
  };

  # See full reference at https://devenv.sh/reference/options/
}
