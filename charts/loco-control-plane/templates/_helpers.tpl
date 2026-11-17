{{- define "loco-control-plane.apiURL" -}}
{{- printf "http://%s-api.%s.svc.cluster.local" .Release.Name .Release.Namespace -}}
{{- end -}}

{{- define "loco-control-plane.gatewayRef" -}}
- name: {{ .Values.gateway.name }}
  namespace: {{ .Values.gatewayNamespace }}
{{- end -}}

{{- define "loco-control-plane.ingestRouted" -}}
{{- if and .Values.obsProxy.enabled .Values.obsProxy.ingest.hostname -}}
true
{{- end -}}
{{- end -}}

{{- define "loco-control-plane.listeners" -}}
{{- $listeners := list -}}
{{- if .Values.ui.enabled -}}
{{- $listeners = append $listeners (dict "name" "ui" "hostname" .Values.ui.hostname) -}}
{{- end -}}
{{- if or (not .Values.ui.enabled) (ne .Values.ui.hostname .Values.controlPlaneHostname) -}}
{{- $listeners = append $listeners (dict "name" "api" "hostname" .Values.controlPlaneHostname) -}}
{{- end -}}
{{- if include "loco-control-plane.ingestRouted" . -}}
{{- $listeners = append $listeners (dict "name" "ingest" "hostname" .Values.obsProxy.ingest.hostname) -}}
{{- end -}}
{{- toJson $listeners -}}
{{- end -}}

{{- define "loco-control-plane.issueCertificate" -}}
{{- if and .Values.certManager.enabled .Values.gateway.create (not .Values.gateway.tlsSecretName) -}}
true
{{- end -}}
{{- end -}}

{{- define "loco-control-plane.tlsSecret" -}}
{{- if .Values.gateway.tlsSecretName -}}
{{- .Values.gateway.tlsSecretName -}}
{{- else if .Values.certManager.enabled -}}
{{- printf "%s-tls" .Release.Name -}}
{{- end -}}
{{- end -}}

{{- define "loco-control-plane.clickhouseURL" -}}
{{- $root := .root -}}
{{- if $root.Values.clickhouse.enabled -}}
{{- $user := dict -}}
{{- range $root.Values.clickhouse.clickhouse.users -}}
{{- if hasPrefix $.grant (first .grants) -}}
{{- $user = . -}}
{{- end -}}
{{- end -}}
name: {{ required (printf "no ClickHouse user is granted %s" .grant) $user.password_secret_name | quote }}
key: url
{{- else -}}
name: {{ required (printf "%s.name is required" .path) .secret.name | quote }}
key: {{ required (printf "%s.key is required" .path) .secret.key | quote }}
{{- end -}}
{{- end -}}

{{- define "loco-control-plane.openbaoName" -}}
{{- printf "%s-openbao" .Release.Name -}}
{{- end -}}

{{- define "loco-control-plane.openbaoPeers" -}}
{{- printf "%s-peers.%s.svc.cluster.local" (include "loco-control-plane.openbaoName" .) .Release.Namespace -}}
{{- end -}}

{{- define "loco-control-plane.openbaoNodeURL" -}}
{{- printf "https://%s-%d.%s:%v" (include "loco-control-plane.openbaoName" .root) (int .index) (include "loco-control-plane.openbaoPeers" .root) .root.Values.openbao.port -}}
{{- end -}}

{{- define "loco-control-plane.openbaoURL" -}}
{{- if .Values.openbao.enabled -}}
{{- printf "https://%s.%s.svc.cluster.local:%v" (include "loco-control-plane.openbaoName" .) .Release.Namespace .Values.openbao.port -}}
{{- else -}}
{{- required "openbao.external.address is required when openbao.enabled is false" .Values.openbao.external.address -}}
{{- end -}}
{{- end -}}

{{- define "loco-control-plane.openbaoTLSSecret" -}}
{{- printf "%s-tls" (include "loco-control-plane.openbaoName" .) -}}
{{- end -}}

{{- define "loco-control-plane.openbaoCAFile" -}}
/var/run/secrets/openbao-ca/ca.crt
{{- end -}}

{{- define "loco-control-plane.openbaoCAVolume" -}}
- name: openbao-ca
  secret:
    secretName: {{ include "loco-control-plane.openbaoTLSSecret" . }}
    items:
      - key: ca.crt
        path: ca.crt
{{- end -}}

{{- define "loco-control-plane.openbaoSetupName" -}}
{{- printf "%s-setup" (include "loco-control-plane.openbaoName" .) -}}
{{- end -}}

{{- define "loco-control-plane.openbaoSnapshotName" -}}
{{- printf "%s-snapshot" (include "loco-control-plane.openbaoName" .) -}}
{{- end -}}

{{- define "loco-control-plane.openbaoInitSecret" -}}
{{- printf "%s-init" (include "loco-control-plane.openbaoName" .) -}}
{{- end -}}

{{- define "loco-control-plane.openbaoInitSecretEnv" -}}
- name: INIT_SECRET
  value: {{ include "loco-control-plane.openbaoInitSecret" . }}
- name: NAMESPACE
  value: {{ .Release.Namespace }}
- name: INIT_SECRET_PATCH
  value: /work/init-secret.json
{{- end -}}

{{- define "loco-control-plane.apiServiceAccount" -}}
{{- printf "%s-api" .Release.Name -}}
{{- end -}}

{{- define "loco-control-plane.requireSecret" -}}
{{- $name := required (printf "%s is required" .path) .name -}}
{{- if lookup "v1" "Namespace" "" .root.Release.Namespace -}}
{{- if not (lookup "v1" "Secret" .root.Release.Namespace $name) -}}
{{- fail (printf "%s names Secret %s, which does not exist in namespace %s" .path $name .root.Release.Namespace) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "loco-control-plane.openbaoConfig" -}}
{{- $seal := .Values.openbao.seal -}}
ui = false
disable_mlock = true

listener "tcp" {
  address         = "0.0.0.0:{{ .Values.openbao.port }}"
  cluster_address = "0.0.0.0:{{ .Values.openbao.clusterPort }}"
  tls_cert_file   = "/openbao/tls/tls.crt"
  tls_key_file    = "/openbao/tls/tls.key"
}

storage "raft" {
  path = "/openbao/file"
{{- range $index := until (int .Values.openbao.replicas) }}
  retry_join {
    leader_api_addr     = "{{ include "loco-control-plane.openbaoNodeURL" (dict "root" $ "index" $index) }}"
    leader_ca_cert_file = "/openbao/tls/ca.crt"
  }
{{- end }}
}
{{ if eq $seal.type "static" }}
seal "static" {
  current_key_id = {{ required "openbao.seal.static.keyId is required" $seal.static.keyId | quote }}
  current_key    = "file:///openbao/seal/key"
}
{{- else if eq $seal.type "awskms" }}
seal "awskms" {
  region     = {{ required "openbao.seal.awskms.region is required" $seal.awskms.region | quote }}
  kms_key_id = {{ required "openbao.seal.awskms.kmsKeyId is required" $seal.awskms.kmsKeyId | quote }}
{{- with $seal.awskms.endpoint }}
  endpoint   = {{ . | quote }}
{{- end }}
}
{{- else if eq $seal.type "transit" }}
seal "transit" {
  address    = {{ required "openbao.seal.transit.address is required" $seal.transit.address | quote }}
  mount_path = {{ required "openbao.seal.transit.mount is required" $seal.transit.mount | quote }}
  key_name   = {{ required "openbao.seal.transit.key is required" $seal.transit.key | quote }}
}
{{- end }}
{{- end -}}

{{- define "loco-control-plane.restrictedContainer" -}}
allowPrivilegeEscalation: false
capabilities:
  drop:
    - ALL
{{- end -}}

{{- define "loco-control-plane.openbaoSetupEnv" -}}
- name: BAO_ADDR
  value: {{ include "loco-control-plane.openbaoNodeURL" (dict "root" . "index" 0) | quote }}
- name: BAO_CACERT
  value: {{ include "loco-control-plane.openbaoCAFile" . }}
- name: OPENBAO_INIT_FILE
  value: /work/init.json
{{- end -}}

{{- define "loco-control-plane.openbaoSetupMounts" -}}
- name: work
  mountPath: /work
- name: scripts
  mountPath: /scripts
  readOnly: true
- name: openbao-ca
  mountPath: /var/run/secrets/openbao-ca
  readOnly: true
{{- end -}}

{{- define "loco-control-plane.apiSettings" -}}
{{- $settings := dict }}
{{- range $key, $env := dict "orgsPerUser" "LOCO_LIMIT_ORGS_PER_USER" "workspacesPerOrg" "LOCO_LIMIT_WORKSPACES_PER_ORG" "membersPerOrg" "LOCO_LIMIT_MEMBERS_PER_ORG" "invitationsPerOrg" "LOCO_LIMIT_INVITATIONS_PER_ORG" "environmentsPerWorkspace" "LOCO_LIMIT_ENVIRONMENTS_PER_WORKSPACE" "resourcesPerWorkspace" "LOCO_LIMIT_RESOURCES_PER_WORKSPACE" "webhooksPerWorkspace" "LOCO_LIMIT_WEBHOOKS_PER_WORKSPACE" "tokensPerEntity" "LOCO_LIMIT_TOKENS_PER_ENTITY" "domainsPerResource" "LOCO_LIMIT_DOMAINS_PER_RESOURCE" "secretsPerResource" "LOCO_SECRETS_MAX_PER_SERVICE" "deploymentHistory" "LOCO_LIMIT_DEPLOYMENT_HISTORY" "buildHistory" "LOCO_LIMIT_BUILD_HISTORY" "logRetentionDays" "LOCO_LIMIT_LOG_RETENTION_DAYS" "traceRetentionDays" "LOCO_LIMIT_TRACE_RETENTION_DAYS" "metricRetentionDays" "LOCO_LIMIT_METRIC_RETENTION_DAYS" }}
{{- $value := index $.Values.api.limits $key }}
{{- if and (ne (toString $value) "<nil>") (ne (toString $value) "") }}
{{- $_ := set $settings $env $value }}
{{- end }}
{{- end }}
{{- range $key, $env := dict "cpu" "LOCO_DEFAULT_CPU" "memory" "LOCO_DEFAULT_MEMORY" "minReplicas" "LOCO_DEFAULT_MIN_REPLICAS" "maxReplicas" "LOCO_DEFAULT_MAX_REPLICAS" "cpuTarget" "LOCO_DEFAULT_CPU_TARGET" "pathPrefix" "LOCO_DEFAULT_PATH_PREFIX" "idleTimeout" "LOCO_DEFAULT_IDLE_TIMEOUT" "healthPath" "LOCO_DEFAULT_HEALTH_PATH" "healthInterval" "LOCO_DEFAULT_HEALTH_INTERVAL" "healthTimeout" "LOCO_DEFAULT_HEALTH_TIMEOUT" "healthFailThreshold" "LOCO_DEFAULT_HEALTH_FAIL_THRESHOLD" "healthStartupGracePeriod" "LOCO_DEFAULT_HEALTH_STARTUP_GRACE_PERIOD" "deployAttempts" "LOCO_DEFAULT_DEPLOY_ATTEMPTS" "deployRetryDelay" "LOCO_DEFAULT_DEPLOY_RETRY_DELAY" }}
{{- $value := index $.Values.api.defaults $key }}
{{- if and (ne (toString $value) "<nil>") (ne (toString $value) "") }}
{{- $_ := set $settings $env $value }}
{{- end }}
{{- end }}
{{- range $key, $env := dict "events" "EVENTS_RETENTION" "images" "LOCO_IMAGE_RETENTION" }}
{{- $value := index $.Values.api.retention $key }}
{{- if and (ne (toString $value) "<nil>") (ne (toString $value) "") }}
{{- $_ := set $settings $env $value }}
{{- end }}
{{- end }}
{{- range $key, $env := dict "ttl" "LOCO_CLUSTER_JOIN_TOKEN_TTL" "maxTtl" "LOCO_CLUSTER_JOIN_TOKEN_MAX_TTL" }}
{{- $value := index $.Values.api.joinToken $key }}
{{- if and (ne (toString $value) "<nil>") (ne (toString $value) "") }}
{{- $_ := set $settings $env $value }}
{{- end }}
{{- end }}
{{- toYaml $settings }}
{{- end }}
