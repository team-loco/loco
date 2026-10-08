{{- define "loco-operator.labels" -}}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: {{ .Chart.Name }}
{{- end }}

{{- define "loco-operator.controllerLabels" -}}
{{ include "loco-operator.labels" . }}
app.kubernetes.io/name: loco-controller
{{- end }}

{{- define "loco-operator.buildControllerLabels" -}}
{{ include "loco-operator.labels" . }}
app.kubernetes.io/name: loco-build-controller
{{- end }}

{{- define "loco-operator.buildConfig" -}}
{{- $_ := required "builds.builderImage.tag is required" .Values.builds.builderImage.tag }}
{{- omit .Values.builds "enabled" "controller" "podSecurity" "agentServiceAccount" | toJson }}
{{- end }}

{{- define "loco-operator.image" -}}
{{ .Values.controller.image.repository }}:{{ required "controller.image.tag is required" .Values.controller.image.tag }}
{{- end }}

{{- define "loco-operator.serviceMonitor" -}}
{{- $root := .root }}
apiVersion: monitoring.coreos.com/v1
kind: ServiceMonitor
metadata:
  labels:
    {{- include "loco-operator.labels" $root | nindent 4 }}
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
        {{- if $root.Values.certManager.enable }}
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
