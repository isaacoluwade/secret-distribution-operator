{{- define "sdo.name" -}}
{{- default "secret-distribution-operator" .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "sdo.fullname" -}}
{{- printf "%s" (include "sdo.name" .) | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{- define "sdo.labels" -}}
app.kubernetes.io/name: {{ include "sdo.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end -}}

{{- define "sdo.selectorLabels" -}}
app.kubernetes.io/name: {{ include "sdo.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{- define "sdo.serviceAccountName" -}}
{{- default (include "sdo.fullname" .) .Values.serviceAccount.name -}}
{{- end -}}
