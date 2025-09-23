# spindle secrets with openbao

This document covers setting up Spindle to use OpenBao for secrets
management via OpenBao Proxy instead of the default SQLite backend.

## overview

Spindle now uses OpenBao Proxy for secrets management. The proxy handles
authentication automatically using AppRole credentials, while Spindle
connects to the local proxy instead of directly to the OpenBao server.

This approach provides better security, automatic token renewal, and
simplified application code.

## installation

Install OpenBao from nixpkgs:

```bash
nix shell nixpkgs#openbao   # for a local server
```

## setup

The setup process can is documented for both local development and production.

### local development

Start OpenBao in dev mode:

```bash
bao server -dev -dev-root-token-id="root" -dev-listen-address=127.0.0.1:8201
```

This starts OpenBao on `http://localhost:8201` with a root token.

Set up environment for bao CLI:

```bash
export BAO_ADDR=http://localhost:8200
export BAO_TOKEN=root
```

### production

You would typically use a systemd service with a configuration file. Refer to
[@tangled.org/infra](https://tangled.org/@tangled.org/infra) for how this can be
achieved using Nix.

Then, initialize the bao server:
```bash
bao operator init -key-shares=1 -key-threshold=1
```

This will print out an unseal key and a root key. Save them somewhere (like a password manager). Then unseal the vault to begin setting it up:
```bash
bao operator unseal <unseal_key>
```

All steps below remain the same across both dev and production setups.

### configure openbao server

Create the spindle KV mount:

```bash
bao secrets enable -path=spindle -version=2 kv
```

Set up AppRole authentication and policy:

Create a policy file `spindle-policy.hcl`:

```hcl
# Full access to spindle KV v2 data
path "spindle/data/*" {
  capabilities = ["create", "read", "update", "delete"]
}

# Access to metadata for listing and management
path "spindle/metadata/*" {
  capabilities = ["list", "read", "delete", "update"]
}

# Allow listing at root level
path "spindle/" {
  capabilities = ["list"]
}

# Required for connection testing and health checks
path "auth/token/lookup-self" {
  capabilities = ["read"]
}
```

Apply the policy and create an AppRole:

```bash
bao policy write spindle-policy spindle-policy.hcl
bao auth enable approle
bao write auth/approle/role/spindle \
    token_policies="spindle-policy" \
    token_ttl=1h \
    token_max_ttl=4h \
    bind_secret_id=true \
    secret_id_ttl=0 \
    secret_id_num_uses=0
```

Get the credentials:

```bash
# Get role ID (static)
ROLE_ID=$(bao read -field=role_id auth/approle/role/spindle/role-id)

# Generate secret ID
SECRET_ID=$(bao write -f -field=secret_id auth/approle/role/spindle/secret-id)

echo "Role ID: $ROLE_ID"
echo "Secret ID: $SECRET_ID"
```

### create proxy configuration

Create the credential files:

```bash
# Create directory for OpenBao files
mkdir -p /tmp/openbao

# Save credentials
echo "$ROLE_ID" > /tmp/openbao/role-id
echo "$SECRET_ID" > /tmp/openbao/secret-id
chmod 600 /tmp/openbao/role-id /tmp/openbao/secret-id
```

Create a proxy configuration file `/tmp/openbao/proxy.hcl`:

```hcl
# OpenBao server connection
vault {
  address = "http://localhost:8200"
}

# Auto-Auth using AppRole
auto_auth {
  method "approle" {
    mount_path = "auth/approle"
    config = {
      role_id_file_path   = "/tmp/openbao/role-id"
      secret_id_file_path = "/tmp/openbao/secret-id"
    }
  }

  # Optional: write token to file for debugging
  sink "file" {
    config = {
      path = "/tmp/openbao/token"
      mode = 0640
    }
  }
}

# Proxy listener for Spindle
listener "tcp" {
  address     = "127.0.0.1:8201"
  tls_disable = true
}

# Enable API proxy with auto-auth token
api_proxy {
  use_auto_auth_token = true
}

# Enable response caching
cache {
  use_auto_auth_token = true
}

# Logging
log_level = "info"
```

### start the proxy

Start OpenBao Proxy:

```bash
bao proxy -config=/tmp/openbao/proxy.hcl
```

The proxy will authenticate with OpenBao and start listening on
`127.0.0.1:8201`.

### configure spindle

Set these environment variables for Spindle:

```bash
export SPINDLE_SERVER_SECRETS_PROVIDER=openbao
export SPINDLE_SERVER_SECRETS_OPENBAO_PROXY_ADDR=http://127.0.0.1:8201
export SPINDLE_SERVER_SECRETS_OPENBAO_MOUNT=spindle
```

Start Spindle:

Spindle will now connect to the local proxy, which handles all
authentication automatically.

## production setup for proxy

For production, you'll want to run the proxy as a service:

Place your production configuration in `/etc/openbao/proxy.hcl` with
proper TLS settings for the vault connection.

## verifying setup

Test the proxy directly:

```bash
# Check proxy health
curl -H "X-Vault-Request: true" http://127.0.0.1:8201/v1/sys/health

# Test token lookup through proxy
curl -H "X-Vault-Request: true" http://127.0.0.1:8201/v1/auth/token/lookup-self
```

Test OpenBao operations through the server:

```bash
# List all secrets
bao kv list spindle/

# Add a test secret via Spindle API, then check it exists
bao kv list spindle/repos/

# Get a specific secret
bao kv get spindle/repos/your_repo_path/SECRET_NAME
```

## how it works

- Spindle connects to OpenBao Proxy on localhost (typically port 8200 or 8201)
- The proxy authenticates with OpenBao using AppRole credentials
- All Spindle requests go through the proxy, which injects authentication tokens
- Secrets are stored at `spindle/repos/{sanitized_repo_path}/{secret_key}`
- Repository paths like `did:plc:alice/myrepo` become `did_plc_alice_myrepo`
- The proxy handles all token renewal automatically
- Spindle no longer manages tokens or authentication directly

## troubleshooting

**Connection refused**: Check that the OpenBao Proxy is running and
listening on the configured address.

**403 errors**: Verify the AppRole credentials are correct and the policy
has the necessary permissions.

**404 route errors**: The spindle KV mount probably doesn't exist - run
the mount creation step again.

**Proxy authentication failures**: Check the proxy logs and verify the
role-id and secret-id files are readable and contain valid credentials.

**Secret not found after writing**: This can indicate policy permission
issues. Verify the policy includes both `spindle/data/*` and
`spindle/metadata/*` paths with appropriate capabilities.

Check proxy logs:

```bash
# If running as systemd service
journalctl -u openbao-proxy -f

# If running directly, check the console output
```

Test AppRole authentication manually:

```bash
bao write auth/approle/login \
    role_id="$(cat /tmp/openbao/role-id)" \
    secret_id="$(cat /tmp/openbao/secret-id)"
```
