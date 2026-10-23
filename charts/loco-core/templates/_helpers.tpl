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

{{- define "loco-core.exporterTokenSecret" -}}
{{- dig "exporter" "bearerToken" "secretName" "" .Values.global -}}
{{- end -}}

{{- define "loco-core.exporterTokenKey" -}}
{{- dig "exporter" "bearerToken" "secretKey" "" .Values.global | required "global.exporter.bearerToken.secretKey is required with secretName" -}}
{{- end -}}

{{- define "loco-core.exporterTokenNamespaces" -}}
{{- $node := dig "exporter" "bearerToken" "nodeNamespace" "" .Values.global | required "global.exporter.bearerToken.nodeNamespace is required with secretName" -}}
{{- list .Values.global.observability.namespace $node | uniq | join "," -}}
{{- end -}}
