#!/bin/sh
# Database backups: a gzipped pg_dump every night, kept BACKUP_KEEP_DAYS days
# in /backups and, when S3_BUCKET is set, copied to S3-compatible storage
# (rclone reads its RCLONE_CONFIG_S3_* settings from the environment).
#
#   backup.sh schedule   every day at BACKUP_HOUR_UTC:00 (the container default)
#   backup.sh now        one dump right away
set -u

KEEP_DAYS="${BACKUP_KEEP_DAYS:-14}"
HOUR_UTC="${BACKUP_HOUR_UTC:-0}"
DIR="${BACKUP_DIR:-/backups}"

log() { echo "$(date -u +%Y-%m-%dT%H:%M:%SZ) backup: $*"; }

dump() {
	file="$DIR/${PGDATABASE}-$(date -u +%Y-%m-%d_%H%M).sql.gz"
	# Written to a temporary name first: a half-written dump never looks like a backup.
	if ! (set -o pipefail; pg_dump --no-owner --no-privileges | gzip >"$file.tmp"); then
		rm -f "$file.tmp"
		log "pg_dump failed"
		return 1
	fi
	mv "$file.tmp" "$file"
	log "$file, $(du -h "$file" | cut -f1)"
	find "$DIR" -name '*.sql.gz' -mtime +"$KEEP_DAYS" -delete

	[ -n "${S3_BUCKET:-}" ] || return 0
	remote="s3:$S3_BUCKET/${S3_PREFIX:-savepoint}"
	if ! rclone copy "$file" "$remote"; then
		log "upload to $remote failed"
		return 1
	fi
	rclone delete --min-age "${KEEP_DAYS}d" "$remote" || log "cleanup of $remote failed"
	log "uploaded to $remote"
}

schedule() {
	log "every day at ${HOUR_UTC}:00 UTC, kept ${KEEP_DAYS} days${S3_BUCKET:+, copied to S3}"
	while :; do
		now=$(date -u +%s)
		next=$((now - now % 86400 + HOUR_UTC * 3600))
		[ "$next" -gt "$now" ] || next=$((next + 86400))
		sleep $((next - now))
		dump || true # logged; try again tomorrow
	done
}

case "${1:-schedule}" in
now) dump ;;
schedule) schedule ;;
*)
	echo "usage: backup.sh [schedule|now]" >&2
	exit 2
	;;
esac
