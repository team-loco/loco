{{- define "loco-worker.collectorConfig" -}}
{{- $collector := .collector -}}
{{- $template := "opentelemetry-collector.deploymentConfig" -}}
{{- if eq .name "otel-col-daemon" -}}
{{- $template = "opentelemetry-collector.daemonsetConfig" -}}
{{- end -}}
{{- $config := include $template $collector | fromYaml -}}
{{/* The subchart defines batch; only the persistent exporter queue buffers records. */}}
{{- $_ := unset $config.processors "batch" -}}
{{- $settings := .root.Values.global.platformTelemetry -}}
{{- $export := .root.Values.global.exporter -}}
{{- if and $settings.enabled (eq .name "otel-col-cluster") (or (ne (int $collector.Values.replicaCount) 1) $collector.Values.autoscaling.enabled) -}}
{{- fail "platform telemetry requires one otel-col-cluster replica with autoscaling disabled" -}}
{{- end -}}
{{- if and (eq .name "otel-col-deploy") $collector.Values.autoscaling.enabled -}}
{{- fail "otel-col-deploy runs a fixed replica count; autoscaling would strand the queues of removed replicas" -}}
{{- end -}}
{{- if and $settings.platformOnly (not $settings.enabled) -}}
{{- fail "global.platformTelemetry.platformOnly requires platformTelemetry.enabled" -}}
{{- end -}}
{{- $queueStorage := "file_storage/exporter" -}}
{{- $exporter := dict "endpoint" (required "global.exporter.endpoint is required" $export.endpoint) "headers" $export.headers "sending_queue" (dict "enabled" true "storage" $queueStorage "num_consumers" $export.queue.num_consumers "queue_size" $export.queue.queue_size "block_on_overflow" $export.queue.block_on_overflow "batch" $export.queue.batch) "retry_on_failure" (merge (dict "enabled" true) $export.retry) -}}
{{- $extensions := dict $queueStorage (dict "directory" $export.queue.directory) -}}
{{- if $export.ingestToken.enabled -}}
{{- $authenticator := "bearertokenauth/exporter" -}}
{{- $_ := set $extensions $authenticator (dict "filename" (printf "%s/%s" $export.ingestToken.directory $export.ingestToken.secretKey)) -}}
{{- $_ := set $exporter "auth" (dict "authenticator" $authenticator) -}}
{{- end -}}
{{- $_ := set $config "extensions" (merge (default dict $config.extensions) $extensions) -}}
{{- $_ := set $config.service "extensions" (concat (default list $config.service.extensions) (keys $extensions | sortAlpha) | uniq) -}}
{{- $exporterName := "otlp_http" -}}
{{- $_ := set $config "exporters" (dict $exporterName $exporter) -}}
{{- $platformExporter := $exporterName -}}
{{- if $settings.enabled -}}
{{- if $settings.exporter.endpoint -}}
{{- $platformExporter = "otlp_http/platform" -}}
{{- $own := omit $exporter "auth" -}}
{{- $_ := set $own "endpoint" $settings.exporter.endpoint -}}
{{- $_ := set $own "headers" $settings.exporter.headers -}}
{{- $_ := set $config.exporters $platformExporter $own -}}
{{- end -}}
{{- if and $settings.platformOnly (index .root.Values "otel-col-deploy" "enabled") -}}
{{- fail "platformOnly requires otel-col-deploy.enabled=false" -}}
{{- end -}}
{{- if ne .name "otel-col-deploy" -}}
{{- $namespaces := list -}}
{{- $chartNamespaces := list .root.Release.Namespace -}}
{{- range $collectorName := list "otel-col-deploy" "otel-col-daemon" "otel-col-cluster" -}}
{{- $chartNamespaces = append $chartNamespaces (index $.root.Values $collectorName "namespaceOverride") -}}
{{- end -}}
{{- range concat $settings.namespaces $chartNamespaces | uniq -}}
{{- $namespaces = append $namespaces (regexQuoteMeta .) -}}
{{- end -}}
{{- $pattern := printf "^(%s)$" (join "|" $namespaces) -}}
{{- $filter := printf "resource.attributes[\"k8s.namespace.name\"] != nil and not IsMatch(resource.attributes[\"k8s.namespace.name\"], %s)" ($pattern | toJson) -}}
{{- $_ := set $config.processors "filter/platform" (dict "error_mode" "propagate" "metric_conditions" (list $filter) "log_conditions" (list "resource.attributes[\"k8s.namespace.name\"] == nil" $filter)) -}}
{{- $attributes := list -}}
{{- range $key, $value := dict "loco.installation.id" $settings.installation "k8s.cluster.name" $settings.cluster "deployment.environment.name" $settings.environment "loco.region" .root.Values.global.region "loco.telemetry.scope" "platform" -}}
{{- $attributes = append $attributes (dict "key" $key "value" $value "action" "upsert") -}}
{{- end -}}
{{- $_ := set $config.processors "resource/platform" (dict "attributes" $attributes) -}}
{{- $receivers := list "k8s_cluster" -}}
{{- if eq .name "otel-col-daemon" -}}
{{- $receivers = list "kubeletstats/platform" -}}
{{- $kubelet := deepCopy $config.receivers.kubeletstats -}}
{{- $_ := set $kubelet "collection_interval" $settings.collectionInterval -}}
{{- $_ := set $config.receivers "kubeletstats/platform" $kubelet -}}
{{/* One file receiver fans out to disjoint platform and workload namespace filters. */}}
{{- $podMetadata := index $config.processors "k8s_attributes/pod_uid" -}}
{{- $_ := set $podMetadata.extract "labels" (append $podMetadata.extract.labels (dict "from" "pod" "key" "app.kubernetes.io/name" "tag_name" "loco.platform.service.name")) -}}
{{- $_ := set $config.processors "filter/workload_logs" (dict "error_mode" "propagate" "log_conditions" (list (printf "resource.attributes[\"k8s.namespace.name\"] != nil and IsMatch(resource.attributes[\"k8s.namespace.name\"], %s)" ($pattern | toJson)))) -}}
{{- $_ := set $config.service.pipelines.logs "processors" (append $config.service.pipelines.logs.processors "filter/workload_logs") -}}
{{- $_ := set $config.service.pipelines "logs/platform" (dict "receivers" (list "file_log") "processors" (list "memory_limiter" "k8s_attributes/pod_uid" "filter/platform" "transform/log_severity" "transform/platform_log" "filter/platform_feedback" "resource/platform")) -}}
{{- $excluded := index $config.receivers "file_log" "exclude" | default list -}}
{{- range $collectorName := list "otel-col-daemon" "otel-col-deploy" "otel-col-cluster" -}}
{{- if index $.root.Values $collectorName "enabled" -}}
{{- $other := index $.root.Subcharts $collectorName -}}
{{- $excluded = append $excluded (printf "/var/log/pods/%s_%s*_*/*/*.log" (include "opentelemetry-collector.namespace" $other) (include "opentelemetry-collector.fullname" $other)) -}}
{{- end -}}
{{- end -}}
{{- $_ := set (index $config.receivers "file_log") "exclude" ($excluded | uniq) -}}

{{- else -}}
{{- $receivers = list "k8s_cluster" "otlp/platform" -}}
{{- $_ := set $config.receivers "otlp/platform" (dict "protocols" (dict "http" (dict "endpoint" (printf "${env:MY_POD_IP}:%v" (include "loco-worker.platformOTLPPort" .root))))) -}}
{{- $_ := set $config.service.pipelines "traces/platform" (dict "receivers" (list "otlp/platform") "processors" (list "memory_limiter" "resource/platform")) -}}
{{- $_ := set $config.receivers.k8s_cluster "collection_interval" $settings.collectionInterval -}}
{{- $_ := set $config.receivers.k8s_cluster "node_conditions_to_report" $settings.nodeConditions -}}
{{- $_ := set $config.receivers.k8s_cluster "allocatable_types_to_report" $settings.allocatableTypes -}}
{{- $_ := set $config.processors "transform/platform_event" (dict "error_mode" "propagate" "log_statements" (list (dict "context" "log" "statements" (list "set(resource.attributes[\"k8s.namespace.name\"], body[\"object\"][\"metadata\"][\"namespace\"]) where IsMap(body) and IsMap(body[\"object\"])" "set(resource.attributes[\"k8s.namespace.name\"], body[\"metadata\"][\"namespace\"]) where IsMap(body) and IsMap(body[\"metadata\"])")))) -}}
{{- $_ := set $config.service.pipelines "logs/platform" (dict "receivers" (list "k8sobjects") "processors" (list "memory_limiter" "transform/platform_event" "filter/platform" "resource/platform")) -}}
{{- end -}}
{{- $_ := set $config.service.pipelines "metrics/platform" (dict "receivers" $receivers "processors" (list "memory_limiter" "filter/platform" "resource/platform")) -}}
{{- if $settings.platformOnly -}}
{{- $pipelines := pick $config.service.pipelines "metrics/platform" "logs/platform" "traces/platform" -}}
{{- $_ := set $config.service "pipelines" $pipelines -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- range $name, $pipeline := $config.service.pipelines -}}
{{- $_ := set $pipeline "exporters" (list (ternary $platformExporter $exporterName (hasSuffix "/platform" $name))) -}}
{{- end -}}
{{- toYaml $config -}}
{{- end -}}
