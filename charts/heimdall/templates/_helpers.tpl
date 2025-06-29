{{/*
Expand the name of the chart.
*/}}
{{- define "heimdall.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this (by the DNS naming spec).
If release name contains chart name it will be used as a full name.
*/}}
{{- define "heimdall.fullname" -}}
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
{{- define "heimdall.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "heimdall.labels" -}}
helm.sh/chart: {{ include "heimdall.chart" . }}
{{ include "heimdall.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "heimdall.selectorLabels" -}}
app.kubernetes.io/name: {{ include "heimdall.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "heimdall.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "heimdall.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Component labels and selectors
*/}}
{{- define "heimdall.componentLabels" -}}
app.kubernetes.io/component: {{ . }}
{{- end }}

{{/*
Redis URL
*/}}
{{- define "heimdall.redisUrl" -}}
{{- if .Values.redis.enabled }}
{{- if .Values.redis.auth.enabled }}
redis://{{ .Values.redis.auth.password }}@{{ include "heimdall.fullname" . }}-redis-master:{{ .Values.redis.master.service.ports.redis }}
{{- else }}
redis://{{ include "heimdall.fullname" . }}-redis-master:{{ .Values.redis.master.service.ports.redis }}
{{- end }}
{{- else }}
{{- .Values.externalRedis.url }}
{{- end }}
{{- end }}

{{/*
Elasticsearch URL
*/}}
{{- define "heimdall.elasticsearchUrl" -}}
{{- if .Values.elasticsearch.enabled }}
http://{{ include "heimdall.fullname" . }}-elasticsearch:{{ .Values.elasticsearch.service.ports.restAPI }}
{{- else }}
{{- .Values.externalElasticsearch.url }}
{{- end }}
{{- end }}

{{/*
Service name helpers for each component
*/}}
{{- define "heimdall.collector.name" -}}
{{- printf "%s-collector" (include "heimdall.fullname" .) }}
{{- end }}

{{- define "heimdall.cloner.name" -}}
{{- printf "%s-cloner" (include "heimdall.fullname" .) }}
{{- end }}

{{- define "heimdall.scanner.name" -}}
{{- printf "%s-scanner" (include "heimdall.fullname" .) }}
{{- end }}

{{- define "heimdall.osvScanner.name" -}}
{{- printf "%s-osv-scanner" (include "heimdall.fullname" .) }}
{{- end }}

{{- define "heimdall.coordinator.name" -}}
{{- printf "%s-coordinator" (include "heimdall.fullname" .) }}
{{- end }}

{{- define "heimdall.indexer.name" -}}
{{- printf "%s-indexer" (include "heimdall.fullname" .) }}
{{- end }}

{{- define "heimdall.cleaner.name" -}}
{{- printf "%s-cleaner" (include "heimdall.fullname" .) }}
{{- end }}

{{/*
Image pull secret
*/}}
{{- define "heimdall.imagePullSecrets" -}}
{{- if .Values.global.imagePullSecrets }}
imagePullSecrets:
{{- range .Values.global.imagePullSecrets }}
- name: {{ . }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Security context for containers
*/}}
{{- define "heimdall.containerSecurityContext" -}}
runAsNonRoot: true
runAsUser: 1000
runAsGroup: 1000
allowPrivilegeEscalation: false
capabilities:
  drop:
    - ALL
readOnlyRootFilesystem: false
{{- end }}

{{/*
Security context for pods
*/}}
{{- define "heimdall.podSecurityContext" -}}
runAsNonRoot: true
runAsUser: 1000
runAsGroup: 1000
fsGroup: 1000
{{- end }}