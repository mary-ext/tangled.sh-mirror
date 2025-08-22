# knot self-hosting guide

So you want to run your own knot server? Great! Here are a few prerequisites:

1. A server of some kind (a VPS, a Raspberry Pi, etc.). Preferably running a Linux distribution of some kind.
2. A (sub)domain name. People generally use `knot.example.com`.
3. A valid SSL certificate for your domain.

There's a couple of ways to get started:
* NixOS: refer to
[flake.nix](https://tangled.sh/@tangled.sh/core/blob/master/flake.nix)
* Docker: Documented at
[@tangled.sh/knot-docker](https://tangled.sh/@tangled.sh/knot-docker)
(community maintained: support is not guaranteed!)
* Manual: Documented below.

## manual setup

First, clone this repository:

```
git clone https://tangled.sh/@tangled.sh/core
```

Then, build the `knot` CLI. This is the knot administration and operation tool.
For the purpose of this guide, we're only concerned with these subcommands:

* `knot server`: the main knot server process, typically run as a
supervised service
* `knot guard`: handles role-based access control for git over SSH
(you'll never have to run this yourself)
* `knot keys`: fetches SSH keys associated with your knot; we'll use
this to generate the SSH `AuthorizedKeysCommand`

```
cd core
export CGO_ENABLED=1
go build -o knot ./cmd/knot
```

Next, move the `knot` binary to a location owned by `root` --
`/usr/local/bin/knot` is a good choice:

```
sudo mv knot /usr/local/bin/knot
```

This is necessary because SSH `AuthorizedKeysCommand` requires [really
specific permissions](https://stackoverflow.com/a/27638306). The
`AuthorizedKeysCommand` specifies a command that is run by `sshd` to
retrieve a user's public SSH keys dynamically for authentication. Let's
set that up.

```
sudo tee /etc/ssh/sshd_config.d/authorized_keys_command.conf <<EOF
Match User git
  AuthorizedKeysCommand /usr/local/bin/knot keys -o authorized-keys
  AuthorizedKeysCommandUser nobody
EOF
```

Then, reload `sshd`:

```
sudo systemctl reload ssh
```

Next, create the `git` user. We'll use the `git` user's home directory
to store repositories:

```
sudo adduser git
```

Create `/home/git/.knot.env` with the following, updating the values as
necessary. The `KNOT_SERVER_OWNER` should be set to your
DID, you can find your DID in the [Settings](https://tangled.sh/settings) page.

```
KNOT_REPO_SCAN_PATH=/home/git
KNOT_SERVER_HOSTNAME=knot.example.com
APPVIEW_ENDPOINT=https://tangled.sh
KNOT_SERVER_OWNER=did:plc:foobar
KNOT_SERVER_INTERNAL_LISTEN_ADDR=127.0.0.1:5444
KNOT_SERVER_LISTEN_ADDR=127.0.0.1:5555
```

If you run a Linux distribution that uses systemd, you can use the provided
service file to run the server. Copy
[`knotserver.service`](/systemd/knotserver.service)
to `/etc/systemd/system/`. Then, run:

```
systemctl enable knotserver
systemctl start knotserver
```

The last step is to configure a reverse proxy like Nginx or Caddy to front your
knot. Here's an example configuration for Nginx:

```
server {
    listen 80;
    listen [::]:80;
    server_name knot.example.com;

    location / {
        proxy_pass http://localhost:5555;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
    }

    # wss endpoint for git events
    location /events {
        proxy_set_header   X-Forwarded-For $remote_addr;
        proxy_set_header   Host $http_host;
        proxy_set_header Upgrade websocket;
        proxy_set_header Connection Upgrade;
        proxy_pass http://localhost:5555;
    }
  # additional config for SSL/TLS go here.
}

```

Remember to use Let's Encrypt or similar to procure a certificate for your
knot domain.

You should now have a running knot server! You can finalize
your registration by hitting the `verify` button on the
[/knots](https://tangled.sh/knots) page. This simply creates
a record on your PDS to announce the existence of the knot.

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

Similarly, update your `sshd` `AuthorizedKeysCommand` to use the updated
repository path:

```
sudo tee /etc/ssh/sshd_config.d/authorized_keys_command.conf <<EOF
Match User git
  AuthorizedKeysCommand /usr/local/bin/knot keys -o authorized-keys -git-dir /home/git/repositories
  AuthorizedKeysCommandUser nobody
EOF
```

Make sure to restart your SSH server!

#### MOTD (message of the day)

To configure the MOTD used ("Welcome to this knot!" by default), edit the
`/home/git/motd` file:

```
printf "Hi from this knot!\n" > /home/git/motd
```

Note that you should add a newline at the end if setting a non-empty message
since the knot won't do this for you.
