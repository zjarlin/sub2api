FROM python:3.12-slim
WORKDIR /app
COPY server.py /app/server.py
USER 65534:65534
ENV G2A_LISTEN=0.0.0.0:7877
EXPOSE 7877
CMD ["python", "/app/server.py"]
