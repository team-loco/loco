{{- define "loco-obs.clickhouseUserSecret" -}}
{{- $user := .user -}}
{{- $secret := "" -}}
{{- range .root.Values.clickhouse.clickhouse.users -}}
{{- if eq .name $user -}}
{{- $secret = .password_secret_name -}}
{{- end -}}
{{- end -}}
{{- required (printf "clickhouse user %s needs a password_secret_name in clickhouse.clickhouse.users" $user) $secret -}}
{{- end -}}

{{- define "loco-obs.clickhouseAddress" -}}
{{- $clickhouse := .Values.global.clickhouse -}}
{{- $clickhouse.host | default (printf "clickhouse-%s-clickhouse.%s.svc.cluster.local" .Release.Name .Release.Namespace) }}:{{ $clickhouse.port }}
{{- end -}}

{{- define "loco-obs.clickhouseDSN" -}}
{{- $root := .root -}}
{{- $secret := .existingSecret -}}
{{- if $secret.name }}
- name: {{ .env }}
  valueFrom:
    secretKeyRef:
      name: {{ $secret.name | quote }}
      key: {{ required (printf "%s.key is required" .path) $secret.key | quote }}
{{- else }}
- name: {{ .passwordEnv }}
  valueFrom:
    secretKeyRef:
      name: {{ include "loco-obs.clickhouseUserSecret" (dict "root" $root "user" .user) | quote }}
      key: password
- name: {{ .env }}
  value: "clickhouse://{{ .user }}:$({{ .passwordEnv }})@{{ include "loco-obs.clickhouseAddress" $root }}{{ if $root.Values.global.clickhouse.secure }}?secure=true{{ end }}"
{{- end }}
{{- end -}}
