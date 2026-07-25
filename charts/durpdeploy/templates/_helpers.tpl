{{/*
Expand the name of the chart.
*/}}
{{- define "durpdeploy.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "durpdeploy.fullname" -}}
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
Chart name and version label value.
*/}}
{{- define "durpdeploy.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels — every resource gets these.
*/}}
{{- define "durpdeploy.labels" -}}
helm.sh/chart: {{ include "durpdeploy.chart" . }}
{{ include "durpdeploy.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels — must be stable across upgrades.
*/}}
{{- define "durpdeploy.selectorLabels" -}}
app.kubernetes.io/name: {{ include "durpdeploy.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
ServiceAccount name.
*/}}
{{- define "durpdeploy.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "durpdeploy.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Image reference, with chart-level default for tag.
*/}}
{{- define "durpdeploy.image" -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end }}

{{/*
Postgres Secret name — operator-managed by default, the chart only
renders one when postgres.enabled is true AND no existingSecret is set.
*/}}
{{- define "durpdeploy.postgresSecretName" -}}
{{- if .Values.postgres.existingSecret -}}
{{- .Values.postgres.existingSecret -}}
{{- else -}}
{{- include "durpdeploy.fullname" . -}}-postgres
{{- end -}}
{{- end }}

{{/*
Secret key Secret name.
*/}}
{{- define "durpdeploy.secretKeyName" -}}
{{- if .Values.secretKey.existingSecret -}}
{{- .Values.secretKey.existingSecret -}}
{{- else -}}
{{- include "durpdeploy.fullname" . -}}-secret-key
{{- end -}}
{{- end }}
