{{- define "loco-control-plane.nodeLogs" -}}
{{- if and (hasKey .Subcharts "nodeCollector") .Values.platformTelemetry.logs.enabled -}}
true
{{- end -}}
{{- end -}}

{{- define "loco-control-plane.collectorLabels" -}}
{{- include "opentelemetry-collector.selectorLabels" (index .root.Subcharts .name) -}}
{{- end -}}

{{- define "loco-control-plane.collectorConfig" -}}
{{- $root := .root -}}
{{- $collector := index $root.Subcharts .name -}}
{{- $node := eq .name "nodeCollector" -}}
{{- $configTemplate := "opentelemetry-collector.deploymentConfig" -}}
{{- if $node -}}
{{- $configTemplate = "opentelemetry-collector.daemonsetConfig" -}}
{{- end -}}
{{- $config := include $configTemplate $collector | fromYaml -}}
{{- $settings := $root.Values.platformTelemetry -}}
{{- $exporter := $config.exporters.otlp_http -}}
{{- $_ := set $exporter "endpoint" $settings.exporter.endpoint -}}
{{- $_ := set $exporter "headers" $settings.exporter.headers -}}
{{- $extensions := list "health_check" "file_storage/exporter" -}}
{{- if not $settings.exporter.endpoint -}}
{{- $_ := set $exporter "endpoint" (printf "http://%s-system-ingest.%s.svc.cluster.local" $root.Release.Name $root.Release.Namespace) -}}
{{- $_ := set $config.extensions "bearertokenauth/exporter" (dict "filename" "/etc/otelcol-exporter/token") -}}
{{- $_ := set $exporter "auth" (dict "authenticator" "bearertokenauth/exporter") -}}
{{- $extensions = append $extensions "bearertokenauth/exporter" -}}
{{- end -}}
{{- if $node -}}
{{- $_ := set $config.service "pipelines" (pick $config.service.pipelines "logs" "metrics") -}}
{{- $_ := set $config.receivers.kubeletstats "collection_interval" $settings.collectionInterval -}}
{{- if $settings.logs.enabled -}}
{{- $extensions = append $extensions "file_storage" -}}
{{- $_ := set $config "receivers" (pick $config.receivers "file_log" "kubeletstats") -}}
{{- else -}}
{{- $_ := set $config.service "pipelines" (pick $config.service.pipelines "metrics") -}}
{{- $_ := set $config "receivers" (pick $config.receivers "kubeletstats") -}}
{{- end -}}
{{- else -}}
{{- $_ := set $config.receivers.k8s_cluster "collection_interval" $settings.collectionInterval -}}
{{- $_ := set $config.receivers.k8s_cluster "node_conditions_to_report" $settings.nodeConditions -}}
{{- $_ := set $config.receivers.k8s_cluster "allocatable_types_to_report" $settings.allocatableTypes -}}
{{- end -}}
{{- $enabledExtensions := dict -}}
{{- range $extensions -}}
{{- $_ := set $enabledExtensions . (index $config.extensions .) -}}
{{- end -}}
{{- $_ := set $config "extensions" $enabledExtensions -}}
{{- $_ := set $config.service "extensions" $extensions -}}
{{- $_ := set $config "exporters" (dict "otlp_http" $exporter) -}}
{{- $namespaces := list $root.Release.Namespace -}}
{{- range $name := list "nodeCollector" "clusterCollector" -}}
{{- if hasKey $root.Subcharts $name -}}
{{- $namespaces = append $namespaces (index $root.Values $name "namespaceOverride") -}}
{{- end -}}
{{- end -}}
{{- $patterns := list -}}
{{- range concat $settings.namespaces $namespaces | uniq -}}
{{- $patterns = append $patterns (regexQuoteMeta .) -}}
{{- end -}}
{{- $pattern := printf "^(%s)$" (join "|" $patterns) -}}
{{- $outside := printf "resource.attributes[\"k8s.namespace.name\"] != nil and not IsMatch(resource.attributes[\"k8s.namespace.name\"], %s)" ($pattern | toJson) -}}
{{- $_ := set $config.processors "filter/platform_namespace" (dict "error_mode" "propagate" "metric_conditions" (list $outside) "log_conditions" (list $outside)) -}}
{{- $attributes := list -}}
{{- range $key, $value := dict "loco.region" $settings.region "k8s.cluster.name" "control-plane" "loco.telemetry.scope" "platform" -}}
{{- $attributes = append $attributes (dict "key" $key "value" $value "action" "upsert") -}}
{{- end -}}
{{- $_ := set $config.processors "resource/platform" (dict "attributes" $attributes) -}}
{{- $processors := dict -}}
{{- range $pipeline := $config.service.pipelines -}}
{{- range $pipeline.processors -}}
{{- $_ := set $processors . (index $config.processors .) -}}
{{- end -}}
{{- end -}}
{{- $_ := set $config "processors" $processors -}}
{{- toYaml $config -}}
{{- end -}}
