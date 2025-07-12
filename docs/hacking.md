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

## running a knot

An end-to-end knot setup requires setting up a machine with
`sshd`, `AuthorizedKeysCommand`, and git user, which is
quite cumbersome. So the nix flake provides a
`nixosConfiguration` to do so.

To begin, head to `http://localhost:3000` in the browser and
generate a knot secret. Replace the existing secret in
`flake.nix` with the newly generated secret.

You can now start a lightweight NixOS VM using
`nixos-shell` like so:

```bash
nix run .#vm
# or nixos-shell --flake .#vm

# hit Ctrl-a + c + q to exit the VM
```

This starts a knot on port 6000, a spindle on port 6555
with `ssh` exposed on port 2222. You can push repositories
to this VM with this ssh config block on your main machine:

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
