{{- define "loco-core.tlsSecret" -}}
{{- default (printf "%s-tls" .Release.Name) .Values.certManager.existingTLSSecret -}}
{{- end -}}

{{- define "loco-core.gateway" -}}
eg
{{- end -}}

{{- define "loco-core.certificate" -}}
{{- if not .Values.certManager.existingTLSSecret -}}
{{- printf "%s-cert" .Release.Name -}}
{{- end -}}
{{- end -}}

{{- define "loco-core.agentKeySecret" -}}
{{- printf "%s-agent-key" .Release.Name -}}
{{- end -}}

{{- define "loco-core.joinTokenSecret" -}}
{{- $token := .Values.agent.joinToken -}}
{{- if and $token.value $token.existingSecret -}}
{{- fail "set agent.joinToken.value or agent.joinToken.existingSecret, not both" -}}
{{- end -}}
{{- if $token.value -}}
{{- printf "%s-agent-join-token" .Release.Name -}}
{{- else -}}
{{- $token.existingSecret -}}
{{- end -}}
{{- end -}}
