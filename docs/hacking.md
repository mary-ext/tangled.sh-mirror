# hacking on tangled

We highly recommend [installing
nix](https://nixos.org/download/) (the package manager)
before working on the codebase. The nix flake provides a lot
of helpers to get started and most importantly, builds and
dev shells are entirely deterministic.

To set up your dev environment:

```bash
nix develop
```

Non-nix users can look at the `devShell` attribute in the
`flake.nix` file to determine necessary dependencies.

## running the appview

The nix flake also exposes a few `app` attributes (run `nix
flake show` to see a full list of what the flake provides),
one of the apps runs the appview with the `air`
live-reloader:

```bash
TANGLED_DEV=true nix run .#watch-appview

# TANGLED_DB_PATH might be of interest to point to
# different sqlite DBs

# in a separate shell, you can live-reload tailwind
nix run .#watch-tailwind
```

To authenticate with the appview, you will need redis and
OAUTH JWKs to be setup:

```
# oauth jwks should already be setup by the nix devshell:
echo $TANGLED_OAUTH_JWKS
{"crv":"P-256","d":"tELKHYH-Dko6qo4ozYcVPE1ah6LvXHFV2wpcWpi8ab4","kid":"1753352226","kty":"EC","x":"mRzYpLzAGq74kJez9UbgGfV040DxgsXpMbaVsdy8RZs","y":"azqqXzUYywMlLb2Uc5AVG18nuLXyPnXr4kI4T39eeIc"}

# if not, you can set it up yourself:
go build -o genjwks.out ./cmd/genjwks
export TANGLED_OAUTH_JWKS="$(./genjwks.out)"

# run redis in at a new shell to store oauth sessions
redis-server
```

## running knots and spindles

An end-to-end knot setup requires setting up a machine with
`sshd`, `AuthorizedKeysCommand`, and git user, which is
quite cumbersome. So the nix flake provides a
`nixosConfiguration` to do so.

<details>
  <summary><strong>MacOS users will have to setup a Nix Builder first</strong></summary>

  In order to build Tangled's dev VM on macOS, you will
  first need to set up a Linux Nix builder. The recommended
  way to do so is to run a [`darwin.linux-builder`
  VM](https://nixos.org/manual/nixpkgs/unstable/#sec-darwin-builder)
  and to register it in `nix.conf` as a builder for Linux
  with the same architecture as your Mac (`linux-aarch64` if
  you are using Apple Silicon).

  > IMPORTANT: You must build `darwin.linux-builder` somewhere other than inside
  > the tangled repo so that it doesn't conflict with the other VM. For example,
  > you can do
  >
  > ```shell
  > cd $(mktemp -d buildervm.XXXXX) && nix run nixpkgs#darwin.linux-builder
  > ```
  >
  > to store the builder VM in a temporary dir.
  >
  > You should read and follow [all the other intructions][darwin builder vm] to
  >  avoid subtle problems.

  Alternatively, you can use any other method to set up a
  Linux machine with `nix` installed that you can `sudo ssh`
  into (in other words, root user on your Mac has to be able
  to ssh into the Linux machine without entering a password)
  and that has the same architecture as your Mac. See
  [remote builder
  instructions](https://nix.dev/manual/nix/2.28/advanced-topics/distributed-builds.html#requirements)
  for how to register such a builder in `nix.conf`.

  > WARNING: If you'd like to use
  > [`nixos-lima`](https://github.com/nixos-lima/nixos-lima) or
  > [Orbstack](https://orbstack.dev/), note that setting them up so that `sudo
  > ssh` works can be tricky. It seems to be [possible with
  > Orbstack](https://github.com/orgs/orbstack/discussions/1669).

</details>

To begin, grab your DID from http://localhost:3000/settings.
Then, set `TANGLED_VM_KNOT_OWNER` and
`TANGLED_VM_SPINDLE_OWNER` to your DID. You can now start a
lightweight NixOS VM like so:

```bash
nix run --impure .#vm

# type `poweroff` at the shell to exit the VM
```

This starts a knot on port 6000, a spindle on port 6555
with `ssh` exposed on port 2222.

Once the services are running, head to
http://localhost:3000/knots and hit verify. It should
verify the ownership of the services instantly if everything
went smoothly.

You can push repositories to this VM with this ssh config
block on your main machine:

```bash
Host nixos-shell
    Hostname localhost
    Port 2222
    User git
    IdentityFile ~/.ssh/my_tangled_key
```

Set up a remote called `local-dev` on a git repo:

```bash
git remote add local-dev git@nixos-shell:user/repo
git push local-dev main
```

### running a spindle

The above VM should already be running a spindle on
`localhost:6555`. Head to http://localhost:3000/spindles and
hit verify. You can then configure each repository to use
this spindle and run CI jobs.

Of interest when debugging spindles:

```
# service logs from journald:
journalctl -xeu spindle

# CI job logs from disk:
ls /var/log/spindle

# debugging spindle db:
sqlite3 /var/lib/spindle/spindle.db

# litecli has a nicer REPL interface:
litecli /var/lib/spindle/spindle.db
```

If for any reason you wish to disable either one of the
services in the VM, modify [nix/vm.nix](/nix/vm.nix) and set
`services.tangled-spindle.enable` (or
`services.tangled-knot.enable`) to `false`.
