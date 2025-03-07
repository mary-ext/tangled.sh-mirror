# tangled

Hello Tanglers! This is the codebase for
[Tangled](https://tangled.sh)&mdash;a code collaboration platform built
on the [AT Protocol](https://atproto.com).

Read the introduction to Tangled [here](https://blog.tangled.sh/intro).

## knot self-hosting guide

So you want to run your own knot server? Great! Here are a few prerequisites:

1. A server of some kind (a VPS, a Raspberry Pi, etc.). Preferably running a Linux of some kind.
2. A (sub)domain name. People generally use `knot.example.com`.
3. A valid SSL certificate for your domain.

There's a couple of ways to get started:
* NixOS: refer to [flake.nix](https://tangled.sh/@tangled.sh/core/blob/master/flake.nix)
* Manual: Documented below.

### manual setup

First, clone this repository:

```
git clone https://tangled.sh/@tangled.sh/core
```

Then, build our binaries (you need to have Go installed):
* `knotserver`: the main server program
* `keyfetch`: utility to fetch ssh pubkeys
* `repoguard`: enforces repository access control

```
cd core
export CGO_ENABLED=1
go build -o knot ./cmd/knotserver
go build -o keyfetch ./cmd/keyfetch
go build -o repoguard ./cmd/repoguard
```

Next, move the `keyfetch` binary to a location owned by `root` -- `/keyfetch` is
a good choice:

```
sudo mv keyfetch /keyfetch
sudo chown root:root /keyfetch
sudo chmod 755 /keyfetch
```

This is necessary because SSH `AuthorizedKeysCommand` requires [really specific
permissions](https://stackoverflow.com/a/27638306). Let's set that up:

```
sudo tee /etc/ssh/sshd_config.d/authorized_keys_command.conf <<EOF
Match User git
  AuthorizedKeysCommand /keyfetch
  AuthorizedKeysCommandUser nobody
EOF
```

Next, create the `git` user:

```
sudo adduser git
```

Copy the `repoguard` binary to the `git` user's home directory:

```
sudo cp repoguard /home/git
sudo chown git:git /home/git/repoguard
```

Now, let's set up the server. Copy the `knot` binary to
`/usr/local/bin/knotserver`. Then, create `/home/git/.knot.env` with the
following, updating the values as necessary. The `KNOT_SERVER_SECRET` can be
obtaind from the [/knots](/knots) page on Tangled.

```
KNOT_REPO_SCAN_PATH=/home/git
KNOT_SERVER_HOSTNAME=knot.example.com
APPVIEW_ENDPOINT=https://tangled.sh
KNOT_SERVER_SECRET=secret
KNOT_SERVER_INTERNAL_LISTEN_ADDR=127.0.0.1:5444
KNOT_SERVER_LISTEN_ADDR=127.0.0.1:5555
```

If you run a Linux distribution that uses systemd, you can use the provided
service file to run the server. Copy
[`knotserver.service`](https://tangled.sh/did:plc:wshs7t2adsemcrrd4snkeqli/core/blob/master/systemd/knotserver.service)
to `/etc/systemd/system/`. Then, run:

```
systemctl enable knotserver
systemctl start knotserver
```

You should now have a running knot server! You can finalize your registration by hitting the
`initialize` button on the [/knots](/knots) page.
