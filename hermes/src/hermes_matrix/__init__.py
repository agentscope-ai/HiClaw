"""hermes-worker custom Matrix platform adapter.

This package overlays hermes-agent's stock ``gateway/platforms/matrix.py``
(which uses ``mautrix``) with a ``matrix-nio``-based implementation that
mirrors the policies of HiClaw's CoPaw worker:

  * DM and group allow-lists
  * @mention requirement in groups (with free-response rooms)
  * Per-room history buffering with markers
  * Vision support gated on the active model's capabilities
  * Optional E2EE via ``matrix-nio[e2e]`` + libolm

The Dockerfile copies ``hermes_matrix/adapter.py`` over
``gateway/platforms/matrix.py`` inside the installed hermes-agent so the
gateway loads our adapter via the standard import path.
"""
