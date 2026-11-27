{{- define "loco-worker.capacityScrape" -}}
config:
  scrape_configs:
    - job_name: loco-capacity
      scrape_interval: {{ .Values.global.platformTelemetry.collectionInterval }}
      scrape_timeout: {{ .Values.global.platformTelemetry.collectionInterval }}
      metrics_path: /capacity
      honor_timestamps: true
      kubernetes_sd_configs:
        - role: pod
          namespaces:
            names: [{{ .Release.Namespace | quote }}]
      relabel_configs:
        - source_labels: [__meta_kubernetes_pod_label_app_kubernetes_io_name]
          action: keep
          regex: {{ if include "loco-worker.runsApps" . }}loco-controller{{ else }}loco-build-controller{{ end }}
        - source_labels: [__meta_kubernetes_pod_phase]
          action: keep
          regex: Running
        - source_labels: [__meta_kubernetes_pod_ip]
          target_label: __address__
          regex: (.+)
          replacement: '$$1:8080'
        - source_labels: [__meta_kubernetes_pod_ip]
          target_label: __address__
          regex: '(.+:.*)'
          replacement: '[$$1]:8080'
      metric_relabel_configs:
        - source_labels: [__name__]
          action: keep
          regex: loco_pod_cpu_request_cores|loco_pod_memory_request_bytes|loco_capacity_sample_pods
{{- end -}}
