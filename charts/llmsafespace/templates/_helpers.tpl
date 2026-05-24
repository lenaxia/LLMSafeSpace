{{/*
Expand the name of the chart.
*/}}
{{- define "llmsafespace.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "llmsafespace.fullname" -}}
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
Common labels.
*/}}
{{- define "llmsafespace.labels" -}}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{ include "llmsafespace.selectorLabels" . }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels.
*/}}
{{- define "llmsafespace.selectorLabels" -}}
app.kubernetes.io/name: {{ include "llmsafespace.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Component-specific labels.
*/}}
{{- define "llmsafespace.api.labels" -}}
{{ include "llmsafespace.labels" . }}
app.kubernetes.io/component: api
{{- end }}

{{- define "llmsafespace.api.selectorLabels" -}}
{{ include "llmsafespace.selectorLabels" . }}
app.kubernetes.io/component: api
{{- end }}

{{- define "llmsafespace.controller.labels" -}}
{{ include "llmsafespace.labels" . }}
app.kubernetes.io/component: controller
{{- end }}

{{- define "llmsafespace.controller.selectorLabels" -}}
{{ include "llmsafespace.selectorLabels" . }}
app.kubernetes.io/component: controller
{{- end }}

{{/*
Service account names.
*/}}
{{- define "llmsafespace.api.serviceAccountName" -}}
{{- if .Values.serviceAccount.api.create }}
{{- default (printf "%s-api" (include "llmsafespace.fullname" .)) .Values.serviceAccount.api.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.api.name }}
{{- end }}
{{- end }}

{{- define "llmsafespace.controller.serviceAccountName" -}}
{{- if .Values.serviceAccount.controller.create }}
{{- default (printf "%s-controller" (include "llmsafespace.fullname" .)) .Values.serviceAccount.controller.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.controller.name }}
{{- end }}
{{- end }}

{{/*
Resolve the name of the credentials secret.
*/}}
{{- define "llmsafespace.secretName" -}}
{{- if .Values.externalSecret.existingSecret }}
{{- .Values.externalSecret.existingSecret }}
{{- else }}
{{- printf "%s-credentials" (include "llmsafespace.fullname" .) }}
{{- end }}
{{- end }}

{{/*
Resolve the namespace where sandbox/workspace CRDs are created. Falls back to
the release namespace if not explicitly set.
*/}}
{{- define "llmsafespace.workspaceNamespace" -}}
{{- default .Release.Namespace .Values.api.config.kubernetes.namespace }}
{{- end }}

{{/*
Resolve image references — defaults the tag to .Chart.AppVersion if omitted.
*/}}
{{- define "llmsafespace.api.image" -}}
{{- $tag := default .Chart.AppVersion .Values.api.image.tag -}}
{{- printf "%s:%s" .Values.api.image.repository $tag -}}
{{- end }}

{{- define "llmsafespace.controller.image" -}}
{{- $tag := default .Chart.AppVersion .Values.controller.image.tag -}}
{{- printf "%s:%s" .Values.controller.image.repository $tag -}}
{{- end }}

{{- define "llmsafespace.migrations.image" -}}
{{- $repo := default .Values.api.image.repository .Values.migrations.image.repository -}}
{{- $tag := default (default .Chart.AppVersion .Values.api.image.tag) .Values.migrations.image.tag -}}
{{- printf "%s:%s" $repo $tag -}}
{{- end }}
