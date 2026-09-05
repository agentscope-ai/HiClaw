"""Tests for CoPaw worker file sync behavior."""

import logging
import subprocess

import pytest

from copaw_worker import sync
from copaw_worker.sync import FileSync


def _file_sync(tmp_path):
    return FileSync(
        endpoint="minio:9000",
        access_key="tt",
        secret_key="secret",
        bucket="agentteams",
        worker_name="tt",
        local_dir=tmp_path,
    )


@pytest.mark.parametrize("storage_provider", ["minio", None])
def test_ensure_alias_sets_static_alias_for_minio_in_k8s_mode(
    monkeypatch, tmp_path, storage_provider
):
    calls = []

    monkeypatch.setenv("AGENTTEAMS_RUNTIME", "k8s")
    if storage_provider is None:
        monkeypatch.delenv("AGENTTEAMS_STORAGE_PROVIDER", raising=False)
    else:
        monkeypatch.setenv("AGENTTEAMS_STORAGE_PROVIDER", storage_provider)
    monkeypatch.delenv(f"MC_HOST_{sync._MC_ALIAS}", raising=False)
    monkeypatch.setattr(sync, "_mc", lambda *args, **_kwargs: calls.append(args))

    fs = _file_sync(tmp_path)

    fs._ensure_alias()

    assert fs._alias_set is True
    assert calls == [
        (
            "alias",
            "set",
            sync._MC_ALIAS,
            "http://minio:9000",
            "tt",
            "secret",
        )
    ]


def test_ensure_alias_requires_mc_host_for_oss_in_k8s_mode(monkeypatch, tmp_path):
    calls = []

    monkeypatch.setenv("AGENTTEAMS_RUNTIME", "k8s")
    monkeypatch.setenv("AGENTTEAMS_STORAGE_PROVIDER", "oss")
    monkeypatch.delenv(f"MC_HOST_{sync._MC_ALIAS}", raising=False)
    monkeypatch.setattr(sync, "_mc", lambda *args, **_kwargs: calls.append(args))

    fs = _file_sync(tmp_path)

    with pytest.raises(RuntimeError, match=f"MC_HOST_{sync._MC_ALIAS}"):
        fs._ensure_alias()

    assert calls == []


def test_ensure_alias_uses_existing_mc_host_for_oss_in_k8s_mode(
    monkeypatch, tmp_path
):
    calls = []

    monkeypatch.setenv("AGENTTEAMS_RUNTIME", "k8s")
    monkeypatch.setenv("AGENTTEAMS_STORAGE_PROVIDER", "oss")
    monkeypatch.setenv(
        f"MC_HOST_{sync._MC_ALIAS}",
        "https://access:secret:token@oss-cn-hangzhou.aliyuncs.com",
    )
    monkeypatch.setattr(sync, "_mc", lambda *args, **_kwargs: calls.append(args))

    fs = _file_sync(tmp_path)

    fs._ensure_alias()

    assert fs._alias_set is True
    assert calls == []


def test_filesync_fallback_uses_copaw_working_dir_parent(monkeypatch, tmp_path):
    working_dir = tmp_path / "alice" / ".copaw"
    monkeypatch.setenv("COPAW_WORKING_DIR", str(working_dir))

    fs = FileSync(
        endpoint="minio:9000",
        access_key="tt",
        secret_key="secret",
        bucket="agentteams",
        worker_name="alice",
    )

    assert fs.local_dir == tmp_path / "alice"


def test_cat_missing_object_is_debug_only(monkeypatch, tmp_path, caplog):
    fs = FileSync(
        endpoint="minio:9000",
        access_key="tt",
        secret_key="secret",
        bucket="agentteams",
        worker_name="tt",
        local_dir=tmp_path,
    )
    monkeypatch.setattr(fs, "_ensure_alias", lambda: None)
    monkeypatch.setattr(
        sync,
        "_mc",
        lambda *_args, **_kwargs: subprocess.CompletedProcess(
            _args,
            1,
            stdout="",
            stderr="mc.bin: <ERROR> Object does not exist.",
        ),
    )
    caplog.set_level(logging.WARNING)

    assert fs._cat("agents/tt/config/mcporter.json") is None
    assert "Object does not exist" not in caplog.text


def test_cat_non_missing_failure_warns(monkeypatch, tmp_path, caplog):
    fs = FileSync(
        endpoint="minio:9000",
        access_key="tt",
        secret_key="secret",
        bucket="agentteams",
        worker_name="tt",
        local_dir=tmp_path,
    )
    monkeypatch.setattr(fs, "_ensure_alias", lambda: None)
    monkeypatch.setattr(
        sync,
        "_mc",
        lambda *_args, **_kwargs: subprocess.CompletedProcess(
            _args,
            1,
            stdout="",
            stderr="AccessDenied: denied",
        ),
    )
    caplog.set_level(logging.WARNING)

    assert fs._cat("agents/tt/openclaw.json") is None
    assert "mc cat failed" in caplog.text
    assert "AccessDenied: denied" in caplog.text
