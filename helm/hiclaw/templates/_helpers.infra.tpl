{{/*
Infrastructure abstraction helpers.

Phase 2 reads the new public values API (`matrix.*`, `gateway.*`, `storage.*`).
The Higress dependency still consumes top-level `higress:` values because the
dependency name remains `higress`; `gateway.higress.enabled` is only the
materialized condition flag.
*/}}

{{- define "hiclaw.matrix.internalURL" -}}
{{- if and (eq .Values.matrix.provider "tuwunel") (eq .Values.matrix.mode "managed") -}}
{{- include "hiclaw.tuwunel.internalURL" . -}}
{{- else -}}
{{- .Values.matrix.internalURL | default "" -}}
{{- end -}}
{{- end }}

{{- define "hiclaw.matrix.serverName" -}}
{{- if and (eq .Values.matrix.provider "tuwunel") (eq .Values.matrix.mode "managed") -}}
{{- include "hiclaw.tuwunel.serverName" . -}}
{{- else -}}
{{- .Values.matrix.serverName | default "" -}}
{{- end -}}
{{- end }}

{{- define "hiclaw.gateway.publicURL" -}}
{{- required "gateway.publicURL is required" .Values.gateway.publicURL -}}
{{- end }}

{{- define "hiclaw.gateway.internalURL" -}}
{{- if and (eq .Values.gateway.provider "higress") (eq .Values.gateway.mode "managed") -}}
{{- include "hiclaw.higress.gatewayURL" . -}}
{{- else -}}
{{- fail (printf "unsupported gateway combination %s/%s" .Values.gateway.provider .Values.gateway.mode) -}}
{{- end -}}
{{- end }}

{{- define "hiclaw.gateway.adminURL" -}}
{{- if and (eq .Values.gateway.provider "higress") (eq .Values.gateway.mode "managed") -}}
{{- include "hiclaw.higress.consoleURL" . -}}
{{- else -}}
{{- fail (printf "unsupported gateway admin combination %s/%s" .Values.gateway.provider .Values.gateway.mode) -}}
{{- end -}}
{{- end }}

{{- define "hiclaw.gateway.higress.enabled" -}}
{{- if and (eq .Values.gateway.provider "higress") (eq .Values.gateway.mode "managed") -}}true{{- else -}}false{{- end -}}
{{- end }}

{{- define "hiclaw.gateway.adminSecretName" -}}
{{- if and (eq .Values.gateway.provider "higress") (eq .Values.gateway.mode "managed") -}}higress-console{{- end -}}
{{- end }}

{{- define "hiclaw.gateway.adminPasswordKey" -}}
{{- if and (eq .Values.gateway.provider "higress") (eq .Values.gateway.mode "managed") -}}adminPassword{{- end -}}
{{- end }}

{{- define "hiclaw.storage.endpoint" -}}
{{- if and (eq .Values.storage.provider "minio") (eq .Values.storage.mode "managed") -}}
{{- include "hiclaw.minio.internalURL" . -}}
{{- else -}}
{{- fail (printf "unsupported storage combination %s/%s" .Values.storage.provider .Values.storage.mode) -}}
{{- end -}}
{{- end }}

{{- define "hiclaw.storage.bucket" -}}
{{- required "storage.bucket is required" .Values.storage.bucket -}}
{{- end }}

{{- define "hiclaw.storage.remoteRoot" -}}
{{- printf "hiclaw/%s" (include "hiclaw.storage.bucket" .) -}}
{{- end }}

{{- define "hiclaw.storage.adminSecretName" -}}
{{- if and (eq .Values.storage.provider "minio") (eq .Values.storage.mode "managed") -}}
{{- include "hiclaw.minio.fullname" . -}}
{{- end -}}
{{- end }}

{{- define "hiclaw.storage.adminAccessKeyKey" -}}
{{- if and (eq .Values.storage.provider "minio") (eq .Values.storage.mode "managed") -}}MINIO_ROOT_USER{{- end -}}
{{- end }}

{{- define "hiclaw.storage.adminSecretKeyKey" -}}
{{- if and (eq .Values.storage.provider "minio") (eq .Values.storage.mode "managed") -}}MINIO_ROOT_PASSWORD{{- end -}}
{{- end }}

{{- define "hiclaw.manager.spec" -}}
{{- $spec := dict
  "model" (.Values.manager.model | default .Values.credentials.defaultModel)
  "runtime" (.Values.manager.runtime | default "openclaw")
  "image" (include "hiclaw.manager.image" .)
  "resources" .Values.manager.resources
-}}
{{- $spec | toJson -}}
{{- end }}
