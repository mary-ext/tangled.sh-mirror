# spindle secrets with openbao

This document covers setting up Spindle to use OpenBao for secrets
management instead of the default SQLite backend.

## installation

Install OpenBao from nixpkgs:

```bash
nix-env -iA nixpkgs.openbao
```

## local development setup

Start OpenBao in dev mode:

```bash
bao server -dev
```

This starts OpenBao on `http://localhost:8200` with a root token. Save
the root token from the output -- you'll need it.

Set up environment for bao CLI:

```bash
export BAO_ADDR=http://localhost:8200
export BAO_TOKEN=hvs.your-root-token-here
```

Create the spindle KV mount:

```bash
bao secrets enable -path=spindle -version=2 kv
```

Set up AppRole authentication:

Create a policy file `spindle-policy.hcl`:

```hcl
path "spindle/data/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}

path "spindle/metadata/*" {
  capabilities = ["list", "read", "delete"]
}

path "spindle/*" {
  capabilities = ["list"]
}
```

Apply the policy and create an AppRole:

```bash
bao policy write spindle-policy spindle-policy.hcl
bao auth enable approle
bao write auth/approle/role/spindle \
    token_policies="spindle-policy" \
    token_ttl=1h \
    token_max_ttl=4h
```

Get the credentials:

```bash
bao read auth/approle/role/spindle/role-id
bao write -f auth/approle/role/spindle/secret-id
```

Configure Spindle:

Set these environment variables for Spindle:

```bash
export SPINDLE_SERVER_SECRETS_PROVIDER=openbao
export SPINDLE_SERVER_SECRETS_OPENBAO_ADDR=http://localhost:8200
export SPINDLE_SERVER_SECRETS_OPENBAO_ROLE_ID=your-role-id-from-above
export SPINDLE_SERVER_SECRETS_OPENBAO_SECRET_ID=your-secret-id-from-above
export SPINDLE_SERVER_SECRETS_OPENBAO_MOUNT=spindle
```

Start Spindle:

Spindle will now use OpenBao for secrets storage with automatic token
renewal.

## verifying setup

List all secrets:

```bash
bao kv list spindle/
```

Add a test secret via Spindle API, then check it exists:

```bash
bao kv list spindle/repos/
```

Get a specific secret:

```bash
bao kv get spindle/repos/your_repo_path/SECRET_NAME
```

## how it works

- Secrets are stored at `spindle/repos/{sanitized_repo_path}/{secret_key}`
- Each repository gets its own namespace
- Repository paths like `at://did:plc:alice/myrepo` become
  `at_did_plc_alice_myrepo`
- The system automatically handles token renewal using AppRole
  authentication
- On shutdown, Spindle cleanly stops the token renewal process

## troubleshooting

**403 errors**: Check that your BAO_TOKEN is set and the spindle mount
exists

**404 route errors**: The spindle KV mount probably doesn't exist - run
the mount creation step again

**Token expired**: The AppRole system should handle this automatically,
but you can check token status with `bao token lookup`
