{{- define "loco-control-plane.apiURL" -}}
{{- printf "http://%s-api.%s.svc.cluster.local" .Release.Name .Release.Namespace -}}
{{- end -}}

{{- define "loco-control-plane.obsProxyURL" -}}
{{- printf "http://%s-obs-proxy-query.%s.svc.cluster.local" .Release.Name .Release.Namespace -}}
{{- end -}}

{{- define "loco-control-plane.gatewayRef" -}}
- name: {{ .Values.gateway.name }}
  namespace: {{ .Values.gatewayNamespace }}
{{- end -}}

{{- define "loco-control-plane.ingestRouted" -}}
{{- if and .Values.obsProxy.enabled .Values.obsProxy.ingest.enabled .Values.obsProxy.ingest.hostname -}}
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

{{- define "loco-control-plane.clickhouseHost" -}}
{{- printf "clickhouse-%s-clickhouse.%s.svc.cluster.local" .Release.Name .Release.Namespace -}}
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

{{- define "loco-control-plane.openbaoInitSecret" -}}
{{- printf "%s-init" (include "loco-control-plane.openbaoName" .) -}}
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
