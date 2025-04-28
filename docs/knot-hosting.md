# knot self-hosting guide

So you want to run your own knot server? Great! Here are a few prerequisites:

1. A server of some kind (a VPS, a Raspberry Pi, etc.). Preferably running a Linux of some kind.
2. A (sub)domain name. People generally use `knot.example.com`.
3. A valid SSL certificate for your domain.

There's a couple of ways to get started:
* NixOS: refer to [flake.nix](https://tangled.sh/@tangled.sh/core/blob/master/flake.nix)
* Docker: Documented below.
* Manual: Documented below.

## docker setup

Clone this repository:

```
git clone https://tangled.sh/@tangled.sh/core
```

Modify the `docker/docker-compose.yml`, specifically the
`KNOT_SERVER_SECRET` and `KNOT_SERVER_HOSTNAME` env vars. Then run:

```
docker compose -f docker/docker-compose.yml up
```

## manual setup

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

Next, move the `keyfetch` binary to a location owned by `root` --
`/usr/local/libexec/tangled-keyfetch` is a good choice:

```
sudo mv keyfetch /usr/local/libexec/tangled-keyfetch
sudo chown root:root /usr/local/libexec/tangled-keyfetch
sudo chmod 755 /usr/local/libexec/tangled-keyfetch
```

This is necessary because SSH `AuthorizedKeysCommand` requires [really specific
permissions](https://stackoverflow.com/a/27638306). Let's set that up:

```
sudo tee /etc/ssh/sshd_config.d/authorized_keys_command.conf <<EOF
Match User git
  AuthorizedKeysCommand /usr/local/libexec/tangled-keyfetch
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

### custom paths

(This section applies to manual setup only. Docker users should edit the mounts
in `docker-compose.yml` instead.)

Right now, the database and repositories of your knot lives in `/home/git`. You
can move these paths if you'd like to store them in another folder. Be careful
when adjusting these paths:

* Stop your knot when moving data (e.g. `systemctl stop knotserver`) to prevent
any possible side effects. Remember to restart it once you're done.
* Make backups before moving in case something goes wrong.
* Make sure the `git` user can read and write from the new paths.

#### database

As an example, let's say the current database is at `/home/git/knotserver.db`,
and we want to move it to `/home/git/database/knotserver.db`.

Copy the current database to the new location. Make sure to copy the `.db-shm`
and `.db-wal` files if they exist.

```
mkdir /home/git/database
cp /home/git/knotserver.db* /home/git/database
```

In the environment (e.g. `/home/git/.knot.env`), set `KNOT_SERVER_DB_PATH` to
the new file path (_not_ the directory):

```
KNOT_SERVER_DB_PATH=/home/git/database/knotserver.db
```

#### repositories

As an example, let's say the repositories are currently in `/home/git`, and we
want to move them into `/home/git/repositories`.

Create the new folder, then move the existing repositories (if there are any):

```
mkdir /home/git/repositories
# move all DIDs into the new folder; these will vary for you!
mv /home/git/did:plc:wshs7t2adsemcrrd4snkeqli /home/git/repositories
```

In the environment (e.g. `/home/git/.knot.env`), update `KNOT_REPO_SCAN_PATH`
to the new directory:

```
KNOT_REPO_SCAN_PATH=/home/git/repositories
```

In your SSH config (e.g. `/etc/ssh/sshd_config.d/authorized_keys_command.conf`),
update the `AuthorizedKeysCommand` line to use the new folder. For example:

```
Match User git
  AuthorizedKeysCommand /usr/local/libexec/tangled-keyfetch -git-dir /home/git/repositories
  AuthorizedKeysCommandUser nobody
```

Make sure to restart your SSH server!

#### git

The keyfetch executable takes multiple arguments to change certain paths. You
can view a full list by running `/usr/local/libexec/tangled-keyfetch -h`.

As an example, if you wanted to change the path to the repoguard executable,
you would edit your SSH config (e.g. `/etc/ssh/sshd_config.d/authorized_keys_command.conf`)
and update the `AuthorizedKeysCommand` line:

```
Match User git
  AuthorizedKeysCommand /usr/local/libexec/tangled-keyfetch -repoguard-path /path/to/repoguard
  AuthorizedKeysCommandUser nobody
```

Make sure to restart your SSH server!
