{{- define "loco-worker.tlsSecret" -}}
{{- default (printf "%s-tls" .Release.Name) .Values.certManager.existingTLSSecret -}}
{{- end -}}

{{- define "loco-worker.gateway" -}}
eg
{{- end -}}

{{- define "loco-worker.gatewayClass" -}}
eg
{{- end -}}

{{- define "loco-worker.agentServiceAccount" -}}
{{- printf "%s-agent" .Release.Name -}}
{{- end -}}

{{- define "loco-worker.observabilityNamespace" -}}
{{- index .Values "otel-col-deploy" "namespaceOverride" -}}
{{- end -}}

{{- define "loco-worker.collectorService" -}}
{{- index .Values "otel-col-deploy" "fullnameOverride" -}}
{{- end -}}

{{- define "loco-worker.certificate" -}}
{{- if not .Values.certManager.existingTLSSecret -}}
{{- printf "%s-cert" .Release.Name -}}
{{- end -}}
{{- end -}}

{{- define "loco-worker.agentKeySecret" -}}
{{- printf "%s-agent-key" .Release.Name -}}
{{- end -}}

{{- define "loco-worker.joinTokenSecret" -}}
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

{{- define "loco-worker.labels" -}}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: {{ .Chart.Name }}
{{- end }}

{{- define "loco-worker.controllerLabels" -}}
{{ include "loco-worker.labels" . }}
app.kubernetes.io/name: loco-controller
{{- end }}

{{- define "loco-worker.buildControllerLabels" -}}
{{ include "loco-worker.labels" . }}
app.kubernetes.io/name: loco-build-controller
{{- end }}

{{- define "loco-worker.buildConfig" -}}
{{- omit .Values.builds "enabled" "controller" "podSecurity" | toJson }}
{{- end }}

{{- define "loco-worker.image" -}}
{{ .Values.controller.image.repository }}:{{ .Values.controller.image.tag }}
{{- end }}

{{- define "loco-worker.serviceMonitor" -}}
{{- $root := .root }}
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  labels:
    {{- include "loco-worker.labels" $root | nindent 4 }}
    app.kubernetes.io/name: {{ .name }}
  name: {{ .name }}-metrics
  namespace: {{ $root.Release.Namespace }}
spec:
  endpoints:
    - path: /metrics
      port: https
      scheme: https
      bearerTokenFile: /var/run/secrets/kubernetes.io/serviceaccount/token
      tlsConfig:
        {{- if $root.Values.metrics.certManager.enable }}
        serverName: {{ .name }}-metrics.{{ $root.Release.Namespace }}.svc
        insecureSkipVerify: false
        ca:
          secret:
            name: metrics-server-cert
            key: ca.crt
        cert:
          secret:
            name: metrics-server-cert
            key: tls.crt
        keySecret:
          name: metrics-server-cert
          key: tls.key
        {{- else }}
        insecureSkipVerify: true
        {{- end }}
  selector:
    matchLabels:
      app.kubernetes.io/name: {{ .name }}
{{- end }}
