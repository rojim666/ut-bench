# UT-Bench Agent Sandbox
#
# Purpose:
# - Run CLI agents such as Codex, Claude Code, OpenCode, and CodeBuddy.
# - Includes Go/Java/C++ compilers so agents can compile-verify generated tests.
#
# Coverage, mutation, and full evaluation toolchains live in the main evaluator
# image built from the repository root Dockerfile.

FROM node:22-bookworm-slim AS agent-clis

# Clear proxy vars (same reason as the final stage)
ENV http_proxy=""
ENV https_proxy=""
ENV HTTP_PROXY=""
ENV HTTPS_PROXY=""
ENV no_proxy=""
ENV NO_PROXY=""

ARG NPM_REGISTRY=
ARG EXTRA_NPM_PACKAGES=

RUN if [ -n "${NPM_REGISTRY}" ]; then npm config set registry "${NPM_REGISTRY}"; fi

# Built-in runtimes used by the default agents.yaml.
RUN npm install -g opencode-ai opencode-linux-x64-baseline
RUN npm install -g @tencent-ai/codebuddy-code
RUN npm install -g @anthropic-ai/claude-code
# Pin to 0.42.x: 0.43+ removed `wire_api="chat"` support and forces /v1/responses,
# which国产兼容端点 (MiMo / DeepSeek / 豆包等) 暂不支持。
# 见 https://github.com/openai/codex/discussions/7782
RUN npm install -g @openai/codex@^0.42.0

# Optional Agent CLI selected from the Web installer, for example:
#   --build-arg EXTRA_NPM_PACKAGES="@openai/codex"
RUN if [ -n "${EXTRA_NPM_PACKAGES}" ]; then npm install -g ${EXTRA_NPM_PACKAGES}; fi

FROM node:22-bookworm-slim

# Clear proxy vars that Docker may inject from the host.
# On Windows + Docker Desktop the host's 127.0.0.1 proxy is unreachable
# inside the container, causing every apt-get / npm to fail with ECONNREFUSED.
ENV http_proxy=""
ENV https_proxy=""
ENV HTTP_PROXY=""
ENV HTTPS_PROXY=""
ENV no_proxy=""
ENV NO_PROXY=""

ARG DEBIAN_MIRROR=

COPY --from=agent-clis /usr/local/bin/ /usr/local/bin/
COPY --from=agent-clis /usr/local/lib/ /usr/local/lib/

RUN set -eux; \
    if [ -n "${DEBIAN_MIRROR}" ]; then \
      MIRROR_HOST=$(echo "${DEBIAN_MIRROR}" | sed 's|https\?://||;s|/$||'); \
      if [ -f /etc/apt/sources.list.d/debian.sources ]; then \
        sed -i "s|deb.debian.org/debian|${MIRROR_HOST}|g" /etc/apt/sources.list.d/debian.sources; \
        sed -i "s|security.debian.org/debian-security|${MIRROR_HOST}-security|g" /etc/apt/sources.list.d/debian.sources || true; \
      elif [ -f /etc/apt/sources.list ]; then \
        sed -i "s|deb.debian.org/debian|${MIRROR_HOST}|g" /etc/apt/sources.list; \
        sed -i "s|security.debian.org/debian-security|${MIRROR_HOST}-security|g" /etc/apt/sources.list || true; \
      fi; \
    fi; \
    if apt-get -o Acquire::Retries=5 -o Acquire::http::Timeout=60 update; then \
      apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        git \
        ripgrep \
        curl \
        wget \
        unzip; \
      apt-get install -y --no-install-recommends \
        python3 \
        python3-pip \
        python3-pytest \
        g++ \
        cmake \
        libgtest-dev \
        default-jdk \
        maven; \
      if [ -d /usr/src/gtest ]; then \
        cd /usr/src/gtest && cmake . && make && cp lib/*.a /usr/lib/; \
      fi; \
    else \
      echo "ERROR: apt update failed; cannot install required toolchains"; \
      exit 1; \
    fi; \
    rm -rf /var/lib/apt/lists/*

# Install Go (same version as main evaluator image).
# Download method cascades through curl, wget, and Node.js fetch so that the
# build never depends on a single binary being present.
ARG GO_VERSION=1.24.2
RUN set -eux; \
    tarball="go${GO_VERSION}.linux-amd64.tar.gz"; \
    dest="/tmp/go.tar.gz"; \
    urls="https://go.dev/dl/${tarball} https://dl.google.com/go/${tarball} https://mirrors.aliyun.com/golang/${tarball}"; \
    downloaded=0; \
    for url in ${urls}; do \
      echo "Trying ${url}"; \
      if command -v curl >/dev/null 2>&1 && curl -fL --retry 5 --retry-delay 3 --retry-all-errors --connect-timeout 20 -o "${dest}" "${url}"; then \
        downloaded=1; break; \
      fi; \
      if command -v wget >/dev/null 2>&1 && wget -q --tries=5 --timeout=20 -O "${dest}" "${url}"; then \
        downloaded=1; break; \
      fi; \
      if node -e "const fs=require('fs'),h=require('https');function dl(u){h.get(u,r=>{if(r.statusCode>=300&&r.statusCode<400&&r.headers.location){return dl(r.headers.location)}const f=fs.createWriteStream('${dest}');r.pipe(f);f.on('finish',()=>process.exit(0))}).on('error',()=>process.exit(1))}dl('${url}')" 2>/dev/null && [ -s "${dest}" ]; then \
        downloaded=1; break; \
      fi; \
      rm -f "${dest}"; \
    done; \
    if [ "${downloaded}" -ne 1 ]; then echo "ERROR: failed to download Go from all mirrors" && exit 1; fi; \
    rm -rf /usr/local/go; \
    tar -C /usr/local -xzf "${dest}"; \
    rm "${dest}"
ENV PATH="/usr/local/go/bin:/root/go/bin:${PATH}"
ENV GOPROXY=https://goproxy.cn,direct

RUN useradd -m -s /bin/bash agent \
    && mkdir -p /home/agent/.npm /home/agent/.cache /workspace \
    && chown -R agent:agent /home/agent /workspace

WORKDIR /workspace
ENV PATH="/usr/local/bin:${PATH}"
