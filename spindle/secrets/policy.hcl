# KV v2 data operations  
path "spindle/data/*" {
  capabilities = ["create", "read", "update", "delete", "list"]
}

# KV v2 metadata operations (needed for listing)
path "spindle/metadata/*" {
  capabilities = ["list", "read", "delete"]
}

# Root path access (needed for mount-level operations)
path "spindle/*" {
  capabilities = ["list"]
}

