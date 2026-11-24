{{- define "obs.name" -}}{{ .Chart.Name }}{{- end -}}
{{- define "obs.fullname" -}}{{ printf "%s-%s" .Release.Name .Chart.Name | trunc 63 | trimSuffix "-" }}{{- end -}}
{{- define "obs.labels" -}}
app.kubernetes.io/name: {{ include "obs.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ printf "%s-%s" .Chart.Name .Chart.Version }}
{{- end -}}
{{- define "obs.selector" -}}
app.kubernetes.io/name: {{ include "obs.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}
{{- define "obs.image" -}}
{{- if .Values.image.digest -}}{{ .Values.image.repository }}@{{ .Values.image.digest }}{{- else -}}{{ .Values.image.repository }}:{{ .Values.image.tag | default .Chart.AppVersion }}{{- end -}}
{{- end -}}
{{- define "obs.env" -}}
{{- range $k, $v := .Values.env }}
- name: {{ $k }}
  value: {{ $v | quote }}
{{- end }}
- name: OBS_STORE_DRIVER
  value: {{ .Values.database.driver | default "postgres" | quote }}
- name: OBS_DATABASE_URL
  valueFrom:
    secretKeyRef:
      name: {{ .Values.database.existingSecret }}
      key: {{ .Values.database.urlKey }}
- name: OBS_SECRET_DIR
  value: {{ .Values.providerSecrets.mountPath | quote }}
{{- end -}}
{{- define "obs.volumes" -}}
- name: tmp
  emptyDir: {}
{{- if .Values.providerSecrets.existingSecret }}
- name: provider-secrets
  secret:
    secretName: {{ .Values.providerSecrets.existingSecret }}
    defaultMode: 0400
{{- end }}
{{- end -}}
{{- define "obs.volumeMounts" -}}
- name: tmp
  mountPath: /tmp
{{- if .Values.providerSecrets.existingSecret }}
- name: provider-secrets
  mountPath: {{ .Values.providerSecrets.mountPath }}
  readOnly: true
{{- end }}
{{- end -}}
