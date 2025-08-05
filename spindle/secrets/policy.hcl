# Allow full access to the spindle KV mount
path "spindle/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}

path "spindle/data/*" {
  capabilities = ["create", "read", "update", "delete"]
}

path "spindle/metadata/*" {
  capabilities = ["list", "read", "delete"]
}

# Allow listing mounts (for connection testing)
path "sys/mounts" {
  capabilities = ["read"]
}

# Allow token self-lookup (for health checks)
path "auth/token/lookup-self" {
  capabilities = ["read"]
}
