{{- define "loco-obs.collectorConfig" -}}
{{- $template := "opentelemetry-collector.deploymentConfig" -}}
{{- if eq .name "otel-col-daemon" -}}
{{- $template = "opentelemetry-collector.daemonsetConfig" -}}
{{- end -}}
{{- include $template .collector -}}
{{- end -}}
