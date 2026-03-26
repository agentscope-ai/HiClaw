{{/*
Expand the name of the chart.
*/}}
{{- define "hiclaw.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "hiclaw.fullname" -}}
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
{{- define "hiclaw.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "hiclaw.labels" -}}
helm.sh/chart: {{ include "hiclaw.chart" . }}
{{ include "hiclaw.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "hiclaw.selectorLabels" -}}
app.kubernetes.io/name: {{ include "hiclaw.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app: hiclaw-manager
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "hiclaw.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (printf "%s-manager" (include "hiclaw.fullname" .)) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Worker Pod ServiceAccount (OSS-only RAM role via RRSA — not the Manager role).
*/}}
{{- define "hiclaw.workerServiceAccountName" -}}
{{- if .Values.workerServiceAccount.name }}
{{- .Values.workerServiceAccount.name }}
{{- else if .Values.workerServiceAccount.create }}
{{- printf "%s-worker" (include "hiclaw.fullname" .) }}
{{- end }}
{{- end }}

{{/*
Namespace for all namespaced resources
*/}}
{{- define "hiclaw.namespace" -}}
{{- default .Release.Namespace .Values.global.namespace }}
{{- end }}

{{/* NAS 形态：默认 global.platform；可被 tuwunel.persistence.platform 覆盖 */}}
{{- define "hiclaw.persistence.platform" -}}
{{- coalesce .Values.tuwunel.persistence.platform .Values.global.platform }}
{{- end }}

{{/* manager.secret.stringData — HICLAW_REGISTRATION_TOKEN（字面量，无 envFrom Secret 时用） */}}
{{- define "hiclaw.registrationToken.literal" -}}
{{- $s := .Values.manager.secret.stringData | default dict }}
{{- index $s "HICLAW_REGISTRATION_TOKEN" | default "" | toString | trim }}
{{- end }}

{{- define "hiclaw.registrationToken.fromManagerEnv" -}}
{{- index (.Values.manager.env | default dict) "HICLAW_REGISTRATION_TOKEN" | default "" | toString | trim }}
{{- end }}

{{/*
Manager image tag
*/}}
{{- define "hiclaw.imageTag" -}}
{{- default .Chart.AppVersion .Values.image.tag }}
{{- end }}

{{/*
Full manager image reference
*/}}
{{- define "hiclaw.image" -}}
{{- printf "%s:%s" .Values.image.repository (include "hiclaw.imageTag" .) }}
{{- end }}

{{/*
Manager envFrom Secret name: chart-managed (manager.secret) or external (manager.envFromSecret).
Empty string means no envFrom block.
*/}}
{{- define "hiclaw.manager.envFromSecretName" -}}
{{- if and .Values.manager.secret.enabled (gt (len .Values.manager.secret.stringData) 0) }}
{{- default (printf "%s-manager-env" (include "hiclaw.fullname" .)) .Values.manager.secret.name }}
{{- else if .Values.manager.envFromSecret }}
{{- .Values.manager.envFromSecret }}
{{- end }}
{{- end }}

{{/* Tuwunel (homeserver) — always deployed */}}
{{- define "hiclaw.tuwunel.fullname" -}}
{{- printf "%s-tuwunel" (include "hiclaw.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* In-cluster DNS: Tuwunel Service (same namespace). No NLB/API gateway required for Pod↔Pod Matrix traffic. */}}
{{- define "hiclaw.tuwunel.clusterFQDN" -}}
{{- printf "%s.%s.svc.cluster.local" (include "hiclaw.tuwunel.fullname" .) (include "hiclaw.namespace" .) }}
{{- end }}

{{- define "hiclaw.tuwunel.internalMatrixURL" -}}
{{- printf "http://%s:%d" (include "hiclaw.tuwunel.clusterFQDN" .) (.Values.tuwunel.service.port | int) }}
{{- end }}

{{/* MXID server part / CONDUWUIT_SERVER_NAME: FQDN:port (non-443 HTTP) */}}
{{- define "hiclaw.tuwunel.matrixServerName" -}}
{{- printf "%s:%d" (include "hiclaw.tuwunel.clusterFQDN" .) (.Values.tuwunel.service.port | int) }}
{{- end }}

{{- define "hiclaw.tuwunel.selectorLabels" -}}
app.kubernetes.io/name: {{ include "hiclaw.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: tuwunel
{{- end }}

{{- define "hiclaw.tuwunel.labels" -}}
helm.sh/chart: {{ include "hiclaw.chart" . }}
{{ include "hiclaw.tuwunel.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "hiclaw.tuwunel.image" -}}
{{- printf "%s/%s:%s" .Values.tuwunel.image.registry .Values.tuwunel.image.repository .Values.tuwunel.image.tag }}
{{- end }}

{{/* ACK 静态 NAS PV：labels.alicloud-pvname 与 PVC selector 对应 */}}
{{- define "hiclaw.tuwunel.pvName" -}}
{{- $generated := printf "%s-pv" (include "hiclaw.tuwunel.fullname" .) }}
{{- default $generated .Values.tuwunel.persistence.pv.name | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "hiclaw.tuwunel.pvAlicloudLabel" -}}
{{- default (include "hiclaw.tuwunel.pvName" .) .Values.tuwunel.persistence.pv.alicloudPvnameLabel | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/* ACK 静态 PV server 与 ACS mountpoint 共用；兼容旧值 pv.server / acs.mountpoint */}}
{{- define "hiclaw.tuwunel.nas.server" -}}
{{- .Values.tuwunel.persistence.nas.server | default .Values.tuwunel.persistence.pv.server | default .Values.tuwunel.persistence.acs.mountpoint }}
{{- end }}

{{/* Element Web：浏览器 Matrix URL；未单独设置时与 HICLAW_AI_GATEWAY_URL 一致 */}}
{{- define "hiclaw.elementWeb.matrixServerURL" -}}
{{- $explicit := index (.Values.elementWeb.env | default dict) "MATRIX_SERVER_URL" | default "" | toString | trim }}
{{- if $explicit }}{{ $explicit }}{{ else }}{{ index (.Values.manager.secret.stringData | default dict) "HICLAW_AI_GATEWAY_URL" | default "" | toString | trim }}{{ end }}
{{- end }}

{{/* Element Web — always deployed */}}
{{- define "hiclaw.elementWeb.fullname" -}}
{{- printf "%s-element-web" (include "hiclaw.fullname" .) | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "hiclaw.elementWeb.selectorLabels" -}}
app.kubernetes.io/name: {{ include "hiclaw.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: element-web
{{- end }}

{{- define "hiclaw.elementWeb.labels" -}}
helm.sh/chart: {{ include "hiclaw.chart" . }}
{{ include "hiclaw.elementWeb.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "hiclaw.elementWeb.image" -}}
{{- printf "%s/%s:%s" .Values.elementWeb.image.registry .Values.elementWeb.image.repository .Values.elementWeb.image.tag }}
{{- end }}

{{/* RRSA: ack-pod-identity-webhook (SA annotation + optional namespace injection) */}}
{{- define "hiclaw.manager.rrsaWebhook" -}}
{{- if and .Values.manager.rrsa.enabled (eq (.Values.manager.rrsa.mode | default "manual") "webhook") .Values.manager.rrsa.roleName -}}true{{- end -}}
{{- end }}

{{/* RRSA: manual projected token + env — same as ACK doc "手动修改应用模板使用RRSA功能" */}}
{{- define "hiclaw.manager.rrsaManual" -}}
{{- if and .Values.manager.rrsa.enabled (eq (.Values.manager.rrsa.mode | default "manual") "manual") .Values.manager.rrsa.manual.roleArn .Values.manager.rrsa.manual.oidcProviderArn -}}true{{- end -}}
{{- end }}

{{- define "hiclaw.worker.rrsaWebhook" -}}
{{- if and .Values.workerServiceAccount.rrsa.enabled (eq (.Values.workerServiceAccount.rrsa.mode | default "manual") "webhook") .Values.workerServiceAccount.rrsa.roleName -}}true{{- end -}}
{{- end }}

{{- define "hiclaw.worker.rrsaManual" -}}
{{- if and .Values.workerServiceAccount.rrsa.enabled (eq (.Values.workerServiceAccount.rrsa.mode | default "manual") "manual") .Values.workerServiceAccount.rrsa.manual.roleArn -}}true{{- end -}}
{{- end }}
