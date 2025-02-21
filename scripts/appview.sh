#!/bin/bash

# Variables
BINARY_NAME="appview"
BINARY_PATH=".bin/app"
SERVER="95.111.206.63"
USER="appview"

# SCP the binary to root's home directory
scp "$BINARY_PATH" root@$SERVER:/root/"$BINARY_NAME"

# SSH into the server and perform the necessary operations
ssh root@$SERVER <<EOF
  set -e  # Exit on error

  # Move binary to /usr/local/bin and set executable permissions
  mv /root/$BINARY_NAME /usr/local/bin/$BINARY_NAME
  chmod +x /usr/local/bin/$BINARY_NAME

  su appview
  cd ~
  ./reset.sh
EOF

echo "Deployment complete."

