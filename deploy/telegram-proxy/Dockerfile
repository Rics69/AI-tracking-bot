# SOCKS5 for the bot through an SSH tunnel to a server abroad: from servers in
# Russia api.telegram.org is often unreachable. Only Telegram traffic uses it.
FROM alpine:3.20
RUN apk add --no-cache openssh-client
COPY tunnel.sh /usr/local/bin/tunnel.sh
ENTRYPOINT ["tunnel.sh"]
