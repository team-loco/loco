{{- define "loco-obs.clickhouseAddress" -}}
{{- $clickhouse := .Values.global.clickhouse -}}
{{- $clickhouse.host | default (printf "clickhouse-%s-clickhouse.%s.svc.cluster.local" .Release.Name .Release.Namespace) }}:{{ $clickhouse.port }}
{{- end -}}
