#!/bin/busybox sh
set -eu

: "${ZOT_HTPASSWD:?ZOT_HTPASSWD must hold the htpasswd file content}"
: "${S3_BUCKET:?S3_BUCKET must name the storage bucket}"
: "${S3_REGION:?S3_REGION must name the storage bucket region}"
: "${S3_ENDPOINT:?S3_ENDPOINT must be the storage bucket endpoint URL}"
: "${AWS_ACCESS_KEY_ID:?AWS_ACCESS_KEY_ID must be the storage bucket access key}"
: "${AWS_SECRET_ACCESS_KEY:?AWS_SECRET_ACCESS_KEY must be the storage bucket secret key}"

umask 077
printf '%s\n' "$ZOT_HTPASSWD" >/etc/zot/htpasswd
/bin/busybox sed \
	-e "s|@S3_BUCKET@|$S3_BUCKET|" \
	-e "s|@S3_REGION@|$S3_REGION|" \
	-e "s|@S3_ENDPOINT@|$S3_ENDPOINT|" \
	/etc/zot/config.json.tmpl >/etc/zot/config.json

set -- /usr/local/bin/zot-linux-*
exec "$1" serve /etc/zot/config.json
