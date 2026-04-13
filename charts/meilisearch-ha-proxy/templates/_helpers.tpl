{{/*
Expand the name of the chart.
*/}}
{{- define "meilisearch-ha-proxy.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "meilisearch-ha-proxy.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "meilisearch-ha-proxy.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels.
*/}}
{{- define "meilisearch-ha-proxy.labels" -}}
helm.sh/chart: {{ include "meilisearch-ha-proxy.chart" . }}
{{ include "meilisearch-ha-proxy.selectorLabels" . }}
app.kubernetes.io/version: {{ .Values.proxy.image.tag | default .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "meilisearch-ha-proxy.selectorLabels" -}}
app.kubernetes.io/name: {{ include "meilisearch-ha-proxy.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
MeiliSearch selector labels.
*/}}
{{- define "meilisearch-ha-proxy.meilisearchLabels" -}}
app.kubernetes.io/name: {{ include "meilisearch-ha-proxy.name" . }}-meilisearch
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Proxy image with tag.
*/}}
{{- define "meilisearch-ha-proxy.proxyImage" -}}
{{ .Values.proxy.image.repository }}:{{ .Values.proxy.image.tag | default .Chart.AppVersion }}
{{- end }}
