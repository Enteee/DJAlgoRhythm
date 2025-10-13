{
  pkgs,
  lib,
  config,
  inputs,
  ...
}:

let
  nixpkgsWithConfig = nixpkgs: pkgs.callPackage (import nixpkgs) { };

  # fix locale
  use-locale = "C.UTF-8";
  custom-locales = pkgs.pkgs.glibcLocalesUtf8.override {
    allLocales = false;
    locales = [ "${use-locale}/UTF-8" ];
  };

in
{
  env = {
    # toggle CI/CD mode
    cicd = lib.mkDefault false;
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

    # poetry might fallback to query a keyring backend which might or might not exist.
    # this is bad. We don't want that. We don't need any keys.
    # https://www.reddit.com/r/learnpython/comments/zcb95y/comment/kdh0aka
    PYTHON_KEYRING_BACKEND = "keyring.backends.fail.Keyring";

  };

  # https://devenv.sh/packages/
  packages =
    with pkgs;
    [
      # git
      git
      git-lfs

      # AI
      claude-code
    ]
    #
    # The following dependencies are made available
    # for interactive devenv's only. Which means they
    # won't be available in the cicd pipeline
    #
    ++ (pkgs.lib.optionals (!config.env.cicd) [
      (
        vscode-with-extensions.override {
          vscodeExtensions =
            with vscode-extensions;
            [
              bbenoist.nix
              mkhl.direnv

              gitlab.gitlab-workflow

              davidanson.vscode-markdownlint

              jebbs.plantuml
            ]
            ++ vscode-utils.extensionsFromVscodeMarketplace [
              {
                name = "hadolint";
                publisher = "exiasr";
                version = "1.1.2";
                sha256 = "sha256-6GO1f8SP4CE8yYl87/tm60FdGHqHsJA4c2B6UKVdpgM=";
              }
            ];
        }
      )
    ]);

  # https://devenv.sh/languages/
  languages.nix.enable = true;
  languages.go.enable = true;

  # https://devenv.sh/git-hooks/
  git-hooks.hooks =
    {
      dos2unix = {
        enable = true;
        entry = "dos2unix";
        args = [
          "--info=c"
        ];
        excludes = [
          ".*/assets/.*"
        ];
      };

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
        };
      };

      yamllint = {
        enable = true;
        excludes = [
          "pnpm-lock.yaml"
          "charts/templates/"
          "charts/charts/"
        ];
        settings = {
          strict = true;
          configData = ''{ extends: default, rules: { document-start: disable, line-length: {max: 165} } }'';
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
      };
    };

  dotenv = {
    enable = true;
    disableHint = true;
  };

  enterShell = ''
    init(){
      (
        set -euo pipefail
        cd '${config.devenv.root}'

        # is interactive shell?
        if tty -s; then
          # - pull lfs artifacts
          # Note: we can not install lfs hooks because,
          # hooks are managed by devenv
          git lfs install --skip-repo
          git lfs pull
          # print help
          devenv-help
        fi

        echo "CI/CD mode: ${builtins.toJSON config.env.cicd}"
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
