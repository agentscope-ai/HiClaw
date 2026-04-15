# -*- coding: utf-8 -*-
"""
HiClaw Matrix channel overlay for CoPaw.

This module replaces CoPaw's built-in matrix channel with HiClaw-specific
enhancements (E2EE, history buffering, mention handling) until they are
merged upstream.
"""
from .channel import MatrixChannel

__all__ = ["MatrixChannel"]
