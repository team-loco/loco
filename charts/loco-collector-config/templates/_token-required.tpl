{{- define "loco-collector.tokenRequired" -}}
{{- if hasKey .Values "exporter" -}}
{{- not .Values.exporter.endpoint -}}
{{- else -}}
{{- .Values.global.exporter.ingestToken.enabled -}}
{{- end -}}
{{- end -}}
