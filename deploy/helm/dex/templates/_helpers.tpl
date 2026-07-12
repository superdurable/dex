{{- define "dex.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "dex.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "dex.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" -}}
{{- end -}}

{{- define "dex.selectorLabels" -}}
app.kubernetes.io/name: {{ include "dex.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "dex.labels" -}}
helm.sh/chart: {{ include "dex.chart" . }}
{{ include "dex.selectorLabels" . }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end -}}

{{- define "dex.headlessServiceName" -}}
{{- printf "%s-headless" (include "dex.fullname" .) -}}
{{- end -}}

{{- define "dex.headlessServiceFQDN" -}}
{{- printf "%s.%s.svc.cluster.local" (include "dex.headlessServiceName" .) .Release.Namespace -}}
{{- end -}}
