{{- define "loco-control-plane.nodeLogs" -}}
{{- if and .Values.platformTelemetry.enabled .Values.platformTelemetry.logs.enabled -}}
true
{{- end -}}
{{- end -}}

{{- define "loco-control-plane.nodeCollectorName" -}}
{{- printf "%s-node-logs" .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "loco-control-plane.nodeCollectorLabels" -}}
app.kubernetes.io/name: loco-control-plane-logs
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "loco-control-plane.nodeCollector" -}}
{{- $library := index .Subcharts "loco-collector-config" -}}
{{- $values := deepCopy (index $library.Values.exports.collectors "otel-col-daemon") -}}
{{- $name := include "loco-control-plane.nodeCollectorName" . -}}
{{- if eq $name .Release.Namespace -}}
{{- fail "the control-plane namespace must differ from its node log collector namespace" -}}
{{- end -}}
{{- $_ := set $values "fullnameOverride" $name -}}
{{- $_ := set $values "namespaceOverride" $name -}}
{{- $exporter := $values.global.exporter -}}
{{- $system := not .Values.platformTelemetry.exporter.endpoint -}}
{{- $_ := set $exporter "endpoint" .Values.platformTelemetry.exporter.endpoint -}}
{{- $_ := set $exporter "headers" .Values.platformTelemetry.exporter.headers -}}
{{- $_ := set $exporter.ingestToken "enabled" $system -}}
{{- $_ := set $exporter.ingestToken "secretName" (include "loco-control-plane.systemIngestSecret" .) -}}
{{- if $system -}}
{{- $_ := set $exporter "endpoint" (printf "http://%s-system-ingest.%s.svc.cluster.local" .Release.Name .Release.Namespace) -}}
{{- end -}}
{{- $settings := dict "enabled" true "platformOnly" true "installation" "" "environment" "" "cluster" "control-plane" "namespaces" list "exporter" (dict "endpoint" "") -}}
{{- $_ := set $values.global "region" (required "platformTelemetry.region is required when platform telemetry is enabled" .Values.platformTelemetry.region) -}}
{{- $_ := set $values.global "platformTelemetry" $settings -}}
{{- $_ := set $values.config "extensions" (merge (dict "file_storage" (dict "directory" "/var/lib/otelcol")) $values.config.extensions) -}}
{{- range $volume := $values.extraVolumes -}}
{{- if eq $volume.name "log-checkpoint-permissions" -}}
{{- $_ := set $volume.hostPath "path" (printf "/var/lib/otelcol/%s" $name) -}}
{{- end -}}
{{- end -}}
{{- toYaml (dict "Release" .Release "Chart" (dict "Name" "control-plane-logs") "Template" .Template "Values" $values) -}}
{{- end -}}

{{- define "loco-control-plane.nodeCollectorConfig" -}}
{{- $collector := include "loco-control-plane.nodeCollector" . | fromYaml -}}
{{- $root := dict "Release" .Release "Values" (dict "global" $collector.Values.global) "Subcharts" (dict "otel-col-daemon" $collector) -}}
{{- include "loco-collector.collectorConfig" (dict "root" $root "collector" $collector "name" "otel-col-daemon" "controlPlane" true) -}}
{{- end -}}
