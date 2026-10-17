FROM python:3.12-slim-bookworm
WORKDIR /app
# 252 构建机直连 files.pythonhosted.org 会长时间读超时；与 edge-media/edge-mail 保持
# 同一约定：构建期走国内镜像，并显式设置超时与重试，避免部署因外网抖动整轮失败。
ARG PIP_INDEX_URL=https://pypi.tuna.tsinghua.edu.cn/simple
COPY requirements.txt ./
RUN pip install --no-cache-dir --index-url "${PIP_INDEX_URL}" --timeout 120 --retries 5 -r requirements.txt
COPY transport.py protocol.py reply.py client.py api.py server.py models.py structured_output.py container_probe.py ./
COPY tool_policy.py tool_calls.py messages.py conversations.py ./
COPY responses.py responses_tools.py responses_sessions.py session_store.py custom_grammar.py ./
COPY native_client.py native_tools.py ./
COPY smoke.py smoke_tools.py smoke_conversations.py smoke_responses.py research_native.py ./
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["python", "server.py"]
