FROM golang:1.26.3-bookworm AS go
FROM node:24-bookworm-slim
ARG CODEX_VERSION=0.136.0
RUN apt-get update && apt-get install -y --no-install-recommends git python3 ca-certificates curl && rm -rf /var/lib/apt/lists/*
COPY --from=go /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:${PATH}"
RUN npm install -g @openai/codex@${CODEX_VERSION}
COPY . /opt/alertops/
RUN chmod +x /opt/alertops/alertops_action.py /opt/alertops/git_askpass.py && ln -s /opt/alertops/alertops_action.py /usr/local/bin/alertops-action
WORKDIR /workspace
ENTRYPOINT ["python3", "/opt/alertops/run.py"]
