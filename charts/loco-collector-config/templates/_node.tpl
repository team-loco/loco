{{- define "loco-collector.namespace" -}}
{{- .Values.namespaceOverride | default .Release.Namespace -}}
{{- end -}}

{{- define "loco-collector.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := .Values.nameOverride | default .Chart.Name -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "loco-collector.lowercaseChartName" -}}
{{- .Chart.Name | lower -}}
{{- end -}}

{{- define "loco-collector.nodeLogConfig" -}}
{{- $config := tpl (toYaml .Values.config) . | fromYaml -}}
{{- $files := dict "include" (list "/var/log/pods/*/*/*.log") "start_at" "end" "retry_on_failure" (dict "enabled" true) "storage" "file_storage" "include_file_path" true "include_file_name" false -}}
{{- $_ := set $config.receivers "file_log" (mustMergeOverwrite $files $config.receivers.file_log) -}}
{{- toYaml $config -}}
{{- end -}}
