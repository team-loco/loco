#!/bin/sh
set -eu

snapshot=/work/openbao.snap

fail() {
	echo "$*" >&2
	exit 1
}

save() {
	: "${BAO_ADDR:?}" "${OPENBAO_KUBERNETES_MOUNT:?}" "${OPENBAO_SNAPSHOT_ROLE:?}" "${OPENBAO_TOKEN_FILE:?}"
	BAO_TOKEN=$(bao write -field=token "auth/$OPENBAO_KUBERNETES_MOUNT/login" role="$OPENBAO_SNAPSHOT_ROLE" jwt=@"$OPENBAO_TOKEN_FILE")
	export BAO_TOKEN
	bao operator raft snapshot save "$snapshot"
	[ -s "$snapshot" ] || fail "the raft snapshot is empty"
}

upload() {
	: "${SNAPSHOT_S3_ENDPOINT:?}" "${SNAPSHOT_S3_BUCKET:?}" "${SNAPSHOT_S3_PREFIX:?}" "${SNAPSHOT_RETENTION:?}"
	: "${RCLONE_CONFIG_SNAPSHOTS_ACCESS_KEY_ID:?}" "${RCLONE_CONFIG_SNAPSHOTS_SECRET_ACCESS_KEY:?}"
	export RCLONE_CONFIG=/dev/null RCLONE_CONFIG_SNAPSHOTS_TYPE=s3 RCLONE_CONFIG_SNAPSHOTS_PROVIDER=Other
	export RCLONE_CONFIG_SNAPSHOTS_ENDPOINT="$SNAPSHOT_S3_ENDPOINT" RCLONE_CONFIG_SNAPSHOTS_REGION="${SNAPSHOT_S3_REGION:-}"
	export RCLONE_CONFIG_SNAPSHOTS_NO_CHECK_BUCKET=true
	directory="snapshots:$SNAPSHOT_S3_BUCKET/$SNAPSHOT_S3_PREFIX"
	name="$(date -u +%Y%m%dT%H%M%SZ).snap"
	rclone copyto "$snapshot" "$directory/$name"
	rclone delete --min-age "$SNAPSHOT_RETENTION" --include '*.snap' "$directory"
	echo "saved $SNAPSHOT_S3_BUCKET/$SNAPSHOT_S3_PREFIX/$name ($(wc -c <"$snapshot") bytes)"
}

[ "$#" -gt 0 ] || fail "usage: $0 save|upload..."
for phase in "$@"; do
	case "$phase" in
	save) save ;;
	upload) upload ;;
	*) fail "unknown phase $phase" ;;
	esac
done
