{{- define "loco-control-plane.apiURL" -}}
{{- printf "http://%s-api.%s.svc.cluster.local" .Release.Name .Release.Namespace -}}
{{- end -}}

{{- define "loco-control-plane.obsProxyURL" -}}
{{- printf "http://%s-obs-proxy.%s.svc.cluster.local" .Release.Name .Release.Namespace -}}
{{- end -}}
