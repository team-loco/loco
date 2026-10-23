{{- define "loco-obs.collectorConfig" -}}
{{- $collector := .collector -}}
{{- $template := "opentelemetry-collector.deploymentConfig" -}}
{{- if eq .name "otel-col-daemon" -}}
{{- $template = "opentelemetry-collector.daemonsetConfig" -}}
{{- end -}}
{{- $config := include $template $collector | fromYaml -}}
{{- $settings := .root.Values.global.platformTelemetry -}}
{{- if and $settings.enabled (eq .name "otel-col-cluster") (or (ne (int $collector.Values.replicaCount) 1) $collector.Values.autoscaling.enabled) -}}
{{- fail "platform telemetry requires one otel-col-cluster replica with autoscaling disabled" -}}
{{- end -}}
{{- if and $settings.platformOnly (not $settings.enabled) -}}
{{- fail "global.platformTelemetry.platformOnly requires platformTelemetry.enabled" -}}
{{- end -}}
{{- if $settings.enabled -}}
{{- if and $settings.platformOnly (index .root.Values "otel-col-deploy" "enabled") -}}
{{- fail "platformOnly requires otel-col-deploy.enabled=false" -}}
{{- end -}}
{{- range $key := list "installation" "cluster" "environment" -}}
{{- $_ := required (printf "global.platformTelemetry.%s is required" $key) (index $settings $key) -}}
{{- end -}}
{{- if not (has $settings.destination (list "clickhouse" "otlp_http")) -}}
{{- fail "global.platformTelemetry.destination must be clickhouse or otlp_http" -}}
{{- end -}}
{{- if not $settings.namespaces -}}
{{- fail "global.platformTelemetry.namespaces must include at least one platform namespace" -}}
{{- end -}}
{{- $namespaces := list -}}
{{- range $settings.namespaces -}}
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
{{- $exporter := deepCopy $config.exporters.clickhouse -}}
{{- if eq $settings.destination "clickhouse" -}}
{{- $exporter = mustMergeOverwrite $exporter (deepCopy $settings.clickhouse) -}}
{{- else -}}
{{- $_ := required "global.platformTelemetry.otlp.endpoint is required" $settings.otlp.endpoint -}}
{{- $exporter = deepCopy $settings.otlp -}}
{{- end -}}
{{- $_ := set $exporter "sending_queue" (deepCopy $settings.sendingQueue) -}}
{{- $_ := set $exporter "retry_on_failure" (deepCopy $settings.retry) -}}
{{- $destination := printf "%s/platform" $settings.destination -}}
{{- $_ := set $config.exporters $destination $exporter -}}
{{- $receivers := list "k8s_cluster" -}}
{{- if eq .name "otel-col-daemon" -}}
{{- $receivers = list "kubeletstats/platform" "hostmetrics/platform" -}}
{{- $kubelet := deepCopy $config.receivers.kubeletstats -}}
{{- $_ := set $kubelet "collection_interval" $settings.collectionInterval -}}
{{- $_ := set $config.receivers "kubeletstats/platform" $kubelet -}}
{{- $host := deepCopy $config.receivers.hostmetrics -}}
{{- $_ := set $host "collection_interval" $settings.collectionInterval -}}
{{- $_ := set $config.receivers "hostmetrics/platform" $host -}}
{{- else -}}
{{- $_ := set $config.receivers.k8s_cluster "collection_interval" $settings.collectionInterval -}}
{{- $_ := set $config.receivers.k8s_cluster "node_conditions_to_report" $settings.nodeConditions -}}
{{- $_ := set $config.receivers.k8s_cluster "allocatable_types_to_report" $settings.allocatableTypes -}}
{{- $_ := set $config.processors "transform/platform_event" (dict "error_mode" "propagate" "log_statements" (list (dict "context" "log" "statements" (list "set(resource.attributes[\"k8s.namespace.name\"], body[\"object\"][\"metadata\"][\"namespace\"]) where IsMap(body) and IsMap(body[\"object\"])" "set(resource.attributes[\"k8s.namespace.name\"], body[\"metadata\"][\"namespace\"]) where IsMap(body) and IsMap(body[\"metadata\"])")))) -}}
{{- $_ := set $config.service.pipelines "logs/platform" (dict "receivers" (list "k8sobjects") "processors" (list "memory_limiter" "transform/platform_event" "filter/platform" "resource/platform" "batch") "exporters" (list $destination)) -}}
{{- end -}}
{{- $_ := set $config.service.pipelines "metrics/platform" (dict "receivers" $receivers "processors" (list "memory_limiter" "filter/platform" "resource/platform" "batch") "exporters" (list $destination)) -}}
{{- if $settings.platformOnly -}}
{{- $pipelines := pick $config.service.pipelines "metrics/platform" "logs/platform" -}}
{{- $_ := set $config.service "pipelines" $pipelines -}}
{{- $_ := set $config "exporters" (dict $destination $exporter) -}}
{{- end -}}
{{- end -}}
{{- toYaml $config -}}
{{- end -}}
