# pg_dump of the same major version as the database, plus rclone for the
# optional copy to S3-compatible storage.
FROM postgres:17-alpine
RUN apk add --no-cache rclone
COPY backup.sh /usr/local/bin/backup.sh
ENTRYPOINT ["backup.sh"]
CMD ["schedule"]
