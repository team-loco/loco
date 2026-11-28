{{- define "loco-control-plane.nodeLogs" -}}
{{- if and .Values.platformTelemetry.enabled .Values.platformTelemetry.logs.enabled -}}
true
{{- end -}}
{{- end -}}

{{- define "loco-control-plane.nodeCollectorName" -}}
{{- .Values.platformTelemetry.logs.namespaceOverride -}}
{{- end -}}

{{- define "loco-control-plane.nodeCollectorLabels" -}}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: agent-collector
{{- end -}}

{{- define "loco-control-plane.nodeCollectorConfig" -}}
{{- $collector := index .Subcharts.platformTelemetry.Subcharts "logs" -}}
{{- $config := include "opentelemetry-collector.daemonsetConfig" $collector | fromYaml -}}
{{- $settings := .Values.platformTelemetry -}}
{{- $exporter := $config.exporters.otlp_http -}}
{{- $_ := set $exporter "endpoint" $settings.exporter.endpoint -}}
{{- $_ := set $exporter "headers" $settings.exporter.headers -}}
{{- $extensions := list "health_check" "file_storage" "file_storage/exporter" -}}
{{- if not $settings.exporter.endpoint -}}
{{- $_ := set $exporter "endpoint" (printf "http://%s-system-ingest.%s.svc.cluster.local" .Release.Name .Release.Namespace) -}}
{{- $_ := set $config.extensions "bearertokenauth/exporter" (dict "filename" "/etc/otelcol-exporter/token") -}}
{{- $_ := set $exporter "auth" (dict "authenticator" "bearertokenauth/exporter") -}}
{{- $extensions = append $extensions "bearertokenauth/exporter" -}}
{{- end -}}
{{- $_ := set $config "exporters" (dict "otlp_http" $exporter) -}}
{{- $_ := set $config.service "extensions" $extensions -}}
{{- $_ := set $config.service "pipelines" (pick $config.service.pipelines "logs") -}}
{{- $_ := set $config "receivers" (pick $config.receivers "file_log") -}}
{{- $attributes := list -}}
{{- range $key, $value := dict "loco.region" $settings.region "k8s.cluster.name" "control-plane" "loco.telemetry.scope" "platform" -}}
{{- $attributes = append $attributes (dict "key" $key "value" $value "action" "upsert") -}}
{{- end -}}
{{- $_ := set $config.processors "resource/platform" (dict "attributes" $attributes) -}}
{{- $_ := unset $config.processors "batch" -}}
{{- toYaml $config -}}
{{- end -}}
