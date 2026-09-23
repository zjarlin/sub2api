FROM python:3.12-slim-bookworm
WORKDIR /app
COPY requirements.txt ./
RUN pip install --no-cache-dir -r requirements.txt
COPY transport.py protocol.py reply.py client.py api.py server.py models.py structured_output.py container_probe.py ./
COPY tool_policy.py tool_calls.py messages.py conversations.py ./
COPY smoke.py smoke_tools.py smoke_conversations.py ./
USER 65534:65534
EXPOSE 8080
ENTRYPOINT ["python", "server.py"]
