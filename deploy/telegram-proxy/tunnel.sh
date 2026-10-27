#!/bin/sh
# Serves SOCKS5 on :1080 through SSH to TELEGRAM_PROXY_SSH (user@host). The key
# is /keys/id_ed25519 (deploy/telegram-proxy/keys on the server, see
# docs/deploy.md); the host's key is remembered in /state. When the connection
# drops, ssh exits and Docker restarts the container.
set -eu
: "${TELEGRAM_PROXY_SSH:?set TELEGRAM_PROXY_SSH=user@host in .env}"
exec ssh -N -D 0.0.0.0:1080 \
	-p "${TELEGRAM_PROXY_SSH_PORT:-22}" \
	-i /keys/id_ed25519 \
	-o BatchMode=yes \
	-o UserKnownHostsFile=/state/known_hosts -o StrictHostKeyChecking=accept-new \
	-o ServerAliveInterval=30 -o ServerAliveCountMax=3 \
	-o ExitOnForwardFailure=yes \
	"$TELEGRAM_PROXY_SSH"
