"""真实 ffmpeg 时间轴测试；仅替换模型推理，不需要下载权重。"""
import json
import math
import struct
import sys
import wave
from dataclasses import replace
from pathlib import Path

import httpx
import pytest
from fastapi.testclient import TestClient

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "dub" / "pipeline"))
import dub_video as pipeline
import dub_server
import app.main as main_module
import app.runtime as runtime


def tone(path, seconds=0.5):
    with wave.open(str(path), "wb") as audio:
        audio.setparams((1, 2, 32000, 0, "NONE", "not compressed"))
        audio.writeframes(b"".join(struct.pack("<h", int(8000 * math.sin(2 * math.pi * 440 * i / 32000)))
                                   for i in range(round(seconds * 32000))))


@pytest.fixture
def video(tmp_path):
    path = tmp_path / "input.mp4"
    pipeline.run(["ffmpeg", "-y", "-f", "lavfi", "-i", "color=c=green:s=160x90:r=10:d=4",
                  "-c:v", "libx264", "-pix_fmt", "yuv420p", str(path)])
    return path


def test_silent_timeline_preserves_tail_and_speech_position(video, tmp_path, monkeypatch):
    monkeypatch.setattr(pipeline, "synthesize", lambda text, language, output: tone(output))
    monkeypatch.setattr(pipeline, "transcribe", lambda *args: pytest.fail("timeline must bypass ASR"))
    output = tmp_path / "result.mp4"
    pipeline.dub_video(video, output, tmp_path / "work", {
        "mode": "timeline", "keep_original_audio": True,
        "segments": [{"start": 1, "end": 2, "text": "你好 & hello"}],
    })
    assert pipeline.probe_duration(output) == pytest.approx(4, abs=0.12)
    with wave.open(str(tmp_path / "work/dub_track.wav"), "rb") as audio:
        samples = struct.unpack(f"<{audio.getnframes()}h", audio.readframes(audio.getnframes()))
    assert max(abs(x) for x in samples[:32000]) == 0
    assert max(abs(x) for x in samples[32000:48000]) > 1000
    assert max(abs(x) for x in samples[64000:]) == 0


def test_auto_requires_audio(video, tmp_path):
    with pytest.raises(ValueError, match="auto mode requires an audio track"):
        pipeline.dub_video(video, tmp_path / "out.mp4", tmp_path / "work")


def test_auto_transcribes_audio_and_keeps_full_video(video, tmp_path, monkeypatch):
    audio = tmp_path / "source.wav"
    tone(audio, 4)
    voiced = tmp_path / "voiced.mp4"
    pipeline.run(["ffmpeg", "-y", "-i", str(video), "-i", str(audio), "-c:v", "copy", "-c:a", "aac", str(voiced)])
    called = []
    def transcribe(path, language):
        called.append((pipeline.probe_duration(path), language))
        return [pipeline.Segment(0.5, 2, "hello")]
    monkeypatch.setattr(pipeline, "transcribe", transcribe)
    monkeypatch.setattr(pipeline, "synthesize", lambda text, language, output: tone(output))
    result = pipeline.dub_video(voiced, tmp_path / "out.mp4", tmp_path / "work", {"keep_original_audio": True})
    assert called[0][1] == "zh"
    assert pipeline.probe_duration(result) == pytest.approx(4, abs=0.12)


@pytest.mark.parametrize("segments", [[], [{"start": -1, "end": 1, "text": "x"}],
    [{"start": 1, "end": 5, "text": "x"}], [{"start": 1, "end": 1, "text": "x"}],
    [{"start": 0, "end": float("nan"), "text": "x"}], [{"start": True, "end": 2, "text": "x"}],
    [{"start": 0, "end": 1, "text": " "}],
    [{"start": 0, "end": 2, "text": "x"}, {"start": 1, "end": 3, "text": "y"}],
])
def test_invalid_timeline_is_rejected(segments):
    with pytest.raises(ValueError):
        pipeline.validate_segments(segments, 4)


def test_too_long_speech_is_not_truncated(tmp_path):
    source = tmp_path / "long.wav"
    tone(source, 2)
    with pytest.raises(ValueError, match="speech is too long"):
        pipeline.fit_audio(source, 1, tmp_path / "short.wav")
    assert not (tmp_path / "short.wav").exists()


def test_worker_receives_options_and_cleans_files(monkeypatch):
    observed = {}
    def fake_dub(source, output, workdir, options):
        observed.update(source=source, options=options, content=source.read_bytes())
        output.write_bytes(b"mp4-result")
    monkeypatch.setattr(dub_server, "dub_video", fake_dub)
    with TestClient(dub_server.app) as client:
        result = client.post("/dub", files={"video": ("output.mp4", b"video", "video/mp4")},
                             data={"options": json.dumps({"mode": "timeline", "segments": [{"start": 0, "end": 2, "text": "x&y"}]})})
        assert result.content == b"mp4-result"
        assert result.headers["content-type"] == "video/mp4"
        assert observed["options"]["segments"][0]["text"] == "x&y"
        assert observed["content"] == b"video"
        assert not observed["source"].parent.exists()
        invalid = client.post("/dub", files={"video": ("v.mp4", b"v")}, data={"options": "[]"})
        assert invalid.status_code == 400


@pytest.mark.parametrize("route", ["/videos/dub", "/dub"])
def test_bridge_forwards_multipart_options_and_binary(tmp_path, monkeypatch, route):
    async def upstream(request):
        content = await request.aread()
        assert request.url.path == "/dub"
        assert b'name="video"' in content and b"video-bytes" in content
        assert b'name="options"' in content and b'"mode":"timeline"' in content and b"x&y" in content
        return httpx.Response(200, headers={"content-type": "video/mp4"}, content=b"mp4-result")
    original = httpx.AsyncClient
    monkeypatch.setattr(runtime.httpx, "AsyncClient", lambda **kw: original(transport=httpx.MockTransport(upstream), **kw))
    monkeypatch.setattr(main_module, "config", replace(main_module.config, data_dir=tmp_path, video_upstream_url="http://worker", dubbing_command=""))
    with TestClient(main_module.app) as client:
        result = client.post(route, files={"video": ("output.mp4", b"video-bytes", "video/mp4")},
                             data={"options": json.dumps({"mode": "timeline", "segments": [{"start": 0, "end": 2, "text": "x&y"}]})})
        assert result.status_code == 200
        if route == "/dub":
            assert result.content == b"mp4-result"
        else:
            task = result.json()
            assert task["status"] == "succeeded"
            assert client.get(f"/tasks/{task['task_id']}/content").content == b"mp4-result"


@pytest.mark.parametrize("failure", ["json", "timeout", "http"])
def test_upstream_failure_persists_without_download(tmp_path, monkeypatch, failure):
    async def upstream(request):
        if failure == "timeout":
            raise httpx.ReadTimeout("worker timed out")
        if failure == "http":
            return httpx.Response(400, json={"detail": "bad timeline"})
        return httpx.Response(200, json={"error": "not a video"})
    original = httpx.AsyncClient
    monkeypatch.setattr(runtime.httpx, "AsyncClient", lambda **kw: original(transport=httpx.MockTransport(upstream), **kw))
    monkeypatch.setattr(main_module, "config", replace(main_module.config, data_dir=tmp_path, video_upstream_url="http://worker", dubbing_command=""))
    with TestClient(main_module.app) as client:
        task = client.post("/videos/dub", files={"video": ("v.mp4", b"video", "video/mp4")}).json()
        assert task["status"] == "failed"
        assert "output" not in task
        assert client.get(f"/tasks/{task['task_id']}").json()["error"]
        assert client.get(f"/tasks/{task['task_id']}/content").status_code == 409
