{{- define "loco-worker.collectorConfig" -}}
{{- $collector := .collector -}}
{{- $template := "opentelemetry-collector.deploymentConfig" -}}
{{- if eq .name "otel-col-daemon" -}}
{{- $template = "opentelemetry-collector.daemonsetConfig" -}}
{{- end -}}
{{- $config := include $template $collector | fromYaml -}}
{{- $settings := .root.Values.global.platformTelemetry -}}
{{- $export := .root.Values.global.exporter -}}
{{- if and $settings.enabled (eq .name "otel-col-cluster") (or (ne (int $collector.Values.replicaCount) 1) $collector.Values.autoscaling.enabled) -}}
{{- fail "platform telemetry requires one otel-col-cluster replica with autoscaling disabled" -}}
{{- end -}}
{{- if and $settings.platformOnly (not $settings.enabled) -}}
{{- fail "global.platformTelemetry.platformOnly requires platformTelemetry.enabled" -}}
{{- end -}}
{{- $queueStorage := "file_storage/exporter" -}}
{{- $exporter := dict "endpoint" (required "global.exporter.endpoint is required" $export.endpoint) "headers" $export.headers "sending_queue" (dict "enabled" true "storage" $queueStorage "num_consumers" $export.queue.num_consumers "queue_size" $export.queue.queue_size) "retry_on_failure" (merge (dict "enabled" true) $export.retry) -}}
{{- $extensions := dict $queueStorage (dict "directory" $export.queue.directory) -}}
{{- if $export.bearerToken.enabled -}}
{{- $authenticator := "bearertokenauth/exporter" -}}
{{- $_ := set $extensions $authenticator (dict "filename" (printf "%s/%s" $export.bearerToken.directory $export.bearerToken.secretKey)) -}}
{{- $_ := set $exporter "auth" (dict "authenticator" $authenticator) -}}
{{- end -}}
{{- $_ := set $config "extensions" (merge (default dict $config.extensions) $extensions) -}}
{{- $_ := set $config.service "extensions" (concat (default list $config.service.extensions) (keys $extensions | sortAlpha) | uniq) -}}
{{- $exporterName := "otlp_http" -}}
{{- $_ := set $config "exporters" (dict $exporterName $exporter) -}}
{{- if $settings.enabled -}}
{{- if and $settings.platformOnly (index .root.Values "otel-col-deploy" "enabled") -}}
{{- fail "platformOnly requires otel-col-deploy.enabled=false" -}}
{{- end -}}
{{- range $key := list "installation" "cluster" "environment" -}}
{{- $_ := required (printf "global.platformTelemetry.%s is required" $key) (index $settings $key) -}}
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
{{- else -}}
{{- $_ := set $config.receivers.k8s_cluster "collection_interval" $settings.collectionInterval -}}
{{- $_ := set $config.receivers.k8s_cluster "node_conditions_to_report" $settings.nodeConditions -}}
{{- $_ := set $config.receivers.k8s_cluster "allocatable_types_to_report" $settings.allocatableTypes -}}
{{- $_ := set $config.processors "transform/platform_event" (dict "error_mode" "propagate" "log_statements" (list (dict "context" "log" "statements" (list "set(resource.attributes[\"k8s.namespace.name\"], body[\"object\"][\"metadata\"][\"namespace\"]) where IsMap(body) and IsMap(body[\"object\"])" "set(resource.attributes[\"k8s.namespace.name\"], body[\"metadata\"][\"namespace\"]) where IsMap(body) and IsMap(body[\"metadata\"])")))) -}}
{{- $_ := set $config.service.pipelines "logs/platform" (dict "receivers" (list "k8sobjects") "processors" (list "memory_limiter" "transform/platform_event" "filter/platform" "resource/platform" "batch")) -}}
{{- end -}}
{{- $_ := set $config.service.pipelines "metrics/platform" (dict "receivers" $receivers "processors" (list "memory_limiter" "filter/platform" "resource/platform" "batch")) -}}
{{- if $settings.platformOnly -}}
{{- $pipelines := pick $config.service.pipelines "metrics/platform" "logs/platform" -}}
{{- $_ := set $config.service "pipelines" $pipelines -}}
{{- end -}}
{{- end -}}
{{- end -}}
{{- range $pipeline := $config.service.pipelines -}}
{{- $_ := set $pipeline "exporters" (list $exporterName) -}}
{{- end -}}
{{- toYaml $config -}}
{{- end -}}
