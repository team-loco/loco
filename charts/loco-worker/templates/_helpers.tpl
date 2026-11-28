{{- define "loco-worker.certificate" -}}
{{- printf "%s-%s" (index . 0).Release.Name (index . 1) -}}
{{- end -}}

{{- define "loco-worker.tlsSecret" -}}
{{- default (printf "%s-tls" (include "loco-worker.certificate" .)) (index . 0).Values.certManager.existingTLSSecret -}}
{{- end -}}

{{- define "loco-worker.certificates" -}}
{{- if not .Values.certManager.existingTLSSecret -}}
{{- $names := list -}}
{{- range .Values.global.domain.apps -}}
{{- $names = append $names (include "loco-worker.certificate" (list $ .)) -}}
{{- end -}}
{{- join "," $names -}}
{{- end -}}
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

{{- define "loco-worker.ingestTokenNamespaces" -}}
{{- list (include "loco-worker.observabilityNamespace" .) (index .Values "otel-col-daemon" "namespaceOverride") | uniq | join "," -}}
{{- end -}}

{{- define "loco-worker.collectorService" -}}
{{- index .Values "otel-col-deploy" "fullnameOverride" -}}
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
{{- omit .Values.builds "controller" "podSecurity" "createNamespace" | toJson }}
{{- end }}

{{- define "loco-worker.runsApps" -}}
{{- if has .Values.cluster.role (list "apps" "all") }}true{{ end }}
{{- end }}

{{- /* The agent reports cluster health from this controller: the Application controller, or the build controller on a builds-only cluster. */ -}}
{{- define "loco-worker.healthController" -}}
{{- if include "loco-worker.runsApps" . }}{{ .Values.controller.name }}{{ else }}loco-build-controller{{ end }}
{{- end }}

{{- define "loco-worker.runsBuilds" -}}
{{- if has .Values.cluster.role (list "builds" "all") }}true{{ end }}
{{- end }}

{{- define "loco-worker.image" -}}
{{ .Values.controller.image.repository }}:{{ .Values.controller.image.tag }}
{{- end }}

{{- define "loco-worker.registryAuth" -}}
{{- if eq .Values.registry.auth "token" -}}
{{- if .Values.registry.pullSecretName -}}
{{- fail "registry.pullSecretName applies only to registry.auth static; token mode copies the agent's Secrets" -}}
{{- end -}}
{{- if .Values.builds.pushSecretName -}}
{{- fail "builds.pushSecretName applies only to registry.auth static; token mode mounts the agent's Secrets" -}}
{{- end -}}
{{- end -}}
{{- .Values.registry.auth -}}
{{- end -}}

{{- define "loco-worker.spreadByNode" -}}
- maxSkew: 1
  topologyKey: kubernetes.io/hostname
  whenUnsatisfiable: ScheduleAnyway
  labelSelector:
    matchLabels:
      {{- toYaml . | nindent 6 }}
{{- end -}}

{{- define "loco-worker.platformOTLPPort" -}}
4318
{{- end -}}

{{/* OTLP/HTTP endpoint of the cluster collector's platform pipelines, which Loco's controllers export to. */}}
{{- define "loco-worker.platformOTLPEndpoint" -}}
{{- printf "http://otel-col-cluster-platform.%s.svc.cluster.local:%s" (index .Values "otel-col-cluster" "namespaceOverride") (include "loco-worker.platformOTLPPort" .) -}}
{{- end -}}

{{/* A controller's OTLP endpoint and instance ID under platform telemetry; .env may not set those variables as well. */}}
{{- define "loco-worker.platformOTLPEnv" -}}
{{- if .root.Values.global.platformTelemetry.enabled -}}
{{- range $variable := .env -}}
{{- if regexMatch "^(OTEL_EXPORTER_OTLP_((TRACES|METRICS)_)?(ENDPOINT|HEADERS)|OTEL_RESOURCE_ATTRIBUTES)$" $variable.name -}}
{{- fail (printf "%s sets %s, which global.platformTelemetry sets" $.path $variable.name) -}}
{{- end -}}
{{- end -}}
- name: OTEL_EXPORTER_OTLP_ENDPOINT
  value: {{ include "loco-worker.platformOTLPEndpoint" .root | quote }}
- name: LOCO_POD_UID
  valueFrom:
    fieldRef:
      fieldPath: metadata.uid
- name: OTEL_RESOURCE_ATTRIBUTES
  value: "service.instance.id=$(LOCO_POD_UID),k8s.namespace.name={{ .root.Release.Namespace }}"
{{- end -}}
{{- end -}}
