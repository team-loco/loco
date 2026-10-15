{{- define "loco-core.tlsSecret" -}}
{{- default (printf "%s-tls" .Release.Name) .Values.certManager.existingTLSSecret -}}
{{- end -}}
