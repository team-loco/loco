{{- define "loco-control-plane.apiURL" -}}
{{- printf "http://%s-api.%s.svc.cluster.local" .Release.Name .Release.Namespace -}}
{{- end -}}

{{- define "loco-control-plane.obsProxyURL" -}}
{{- printf "http://%s-obs-proxy-query.%s.svc.cluster.local" .Release.Name .Release.Namespace -}}
{{- end -}}

{{- define "loco-control-plane.gatewayRef" -}}
- name: {{ .Values.gateway.name }}
  namespace: {{ .Values.gatewayNamespace }}
{{- end -}}

{{- define "loco-control-plane.ingestRouted" -}}
{{- if and .Values.obsProxy.enabled .Values.obsProxy.ingest.enabled .Values.obsProxy.ingest.hostname -}}
true
{{- end -}}
{{- end -}}

{{- define "loco-control-plane.clickhouseHost" -}}
{{- printf "clickhouse-%s-clickhouse.%s.svc.cluster.local" .Release.Name .Release.Namespace -}}
{{- end -}}

{{- define "loco-control-plane.clickhouseURL" -}}
{{- $root := .root -}}
{{- if $root.Values.clickhouse.enabled -}}
{{- $user := dict -}}
{{- range $root.Values.clickhouse.clickhouse.users -}}
{{- if hasPrefix $.grant (first .grants) -}}
{{- $user = . -}}
{{- end -}}
{{- end -}}
name: {{ required (printf "no ClickHouse user is granted %s" .grant) $user.password_secret_name | quote }}
key: url
{{- else -}}
name: {{ required (printf "%s.name is required" .path) .secret.name | quote }}
key: {{ required (printf "%s.key is required" .path) .secret.key | quote }}
{{- end -}}
{{- end -}}
