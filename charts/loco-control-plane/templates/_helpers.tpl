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

{{- define "loco-control-plane.listeners" -}}
{{- $listeners := list -}}
{{- if .Values.ui.enabled -}}
{{- $listeners = append $listeners (dict "name" "ui" "hostname" .Values.ui.hostname) -}}
{{- end -}}
{{- if or (not .Values.ui.enabled) (ne .Values.ui.hostname .Values.controlPlaneHostname) -}}
{{- $listeners = append $listeners (dict "name" "api" "hostname" .Values.controlPlaneHostname) -}}
{{- end -}}
{{- if include "loco-control-plane.ingestRouted" . -}}
{{- $listeners = append $listeners (dict "name" "ingest" "hostname" .Values.obsProxy.ingest.hostname) -}}
{{- end -}}
{{- toJson $listeners -}}
{{- end -}}

{{- define "loco-control-plane.issueCertificate" -}}
{{- if and .Values.certManager.enabled .Values.gateway.create (not .Values.gateway.tlsSecretName) -}}
true
{{- end -}}
{{- end -}}

{{- define "loco-control-plane.tlsSecret" -}}
{{- if .Values.gateway.tlsSecretName -}}
{{- .Values.gateway.tlsSecretName -}}
{{- else if .Values.certManager.enabled -}}
{{- printf "%s-tls" .Release.Name -}}
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
