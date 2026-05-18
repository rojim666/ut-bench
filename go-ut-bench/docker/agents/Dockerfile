# UT-Bench Agent 沙箱
#
# 用途：
# - 运行 Codex、Claude Code、OpenCode、CodeBuddy 等 CLI agent。
# - 内置轻量语言工具链，方便 agent 返回前编译或冒烟测试生成用例。
#
# 覆盖率、变异测试和完整评测工具链位于仓库根 Dockerfile 构建的主评测镜像。

FROM node:22-bookworm-slim AS agent-clis

ENV http_proxy=""
ENV https_proxy=""
ENV HTTP_PROXY=""
ENV HTTPS_PROXY=""
ENV no_proxy=""
ENV NO_PROXY=""

ARG NPM_REGISTRY=
ARG EXTRA_NPM_PACKAGES=

RUN if [ -n "${NPM_REGISTRY}" ]; then npm config set registry "${NPM_REGISTRY}"; fi

RUN npm install -g opencode-ai opencode-linux-x64-baseline
RUN npm install -g @tencent-ai/codebuddy-code
RUN npm install -g @anthropic-ai/claude-code
# 固定到 0.42.x：0.43+ 移除了 wire_api="chat" 支持并强制使用 /v1/responses，
# 一些 OpenAI 兼容端点暂不支持该接口。
RUN npm install -g @openai/codex@^0.42.0

RUN if [ -n "${EXTRA_NPM_PACKAGES}" ]; then npm install -g ${EXTRA_NPM_PACKAGES}; fi

FROM golang:1.24-bookworm AS go-runtime

FROM node:22-bookworm-slim

ENV http_proxy=""
ENV https_proxy=""
ENV HTTP_PROXY=""
ENV HTTPS_PROXY=""
ENV no_proxy=""
ENV NO_PROXY=""

ARG DEBIAN_MIRROR=

COPY --from=agent-clis /usr/local/bin/ /usr/local/bin/
COPY --from=agent-clis /usr/local/lib/ /usr/local/lib/
COPY --from=go-runtime /usr/local/go/ /usr/local/go/

# 安装 CLI agent 使用的语言运行时和轻量构建工具。
# 镜像源轮询用于处理 deb.debian.org 较慢或不可达的网络环境。
RUN set -eux; \
    if [ -f /etc/apt/sources.list.d/debian.sources ]; then \
      cp /etc/apt/sources.list.d/debian.sources /tmp/debian.sources.bak; \
    fi; \
    if [ -f /etc/apt/sources.list ]; then \
      cp /etc/apt/sources.list /tmp/sources.list.bak; \
    fi; \
    MIRROR_LIST=""; \
    for m in "${DEBIAN_MIRROR}" "http://mirrors.aliyun.com/debian" "http://repo.huaweicloud.com/debian" "http://mirrors.tuna.tsinghua.edu.cn/debian"; do \
      [ -n "$m" ] && MIRROR_LIST="$MIRROR_LIST $m"; \
    done; \
    APT_OK=0; \
    for MIRROR in $MIRROR_LIST; do \
      echo ">>> Trying mirror: $MIRROR"; \
      if [ -f /tmp/debian.sources.bak ]; then cp /tmp/debian.sources.bak /etc/apt/sources.list.d/debian.sources; fi; \
      if [ -f /tmp/sources.list.bak ]; then cp /tmp/sources.list.bak /etc/apt/sources.list; fi; \
      MIRROR_HOST=$(echo "$MIRROR" | sed 's|https\?://||;s|/*$||'); \
      if [ -f /etc/apt/sources.list.d/debian.sources ]; then \
        sed -i "s|deb.debian.org/debian|${MIRROR_HOST}|g" /etc/apt/sources.list.d/debian.sources; \
        sed -i "s|security.debian.org/debian-security|${MIRROR_HOST}-security|g" /etc/apt/sources.list.d/debian.sources || true; \
      elif [ -f /etc/apt/sources.list ]; then \
        sed -i "s|deb.debian.org/debian|${MIRROR_HOST}|g" /etc/apt/sources.list; \
        sed -i "s|security.debian.org/debian-security|${MIRROR_HOST}-security|g" /etc/apt/sources.list || true; \
      fi; \
      if apt-get -o Acquire::Retries=3 -o Acquire::http::Timeout=30 update; then \
        echo ">>> Mirror OK: $MIRROR"; \
        APT_OK=1; \
        break; \
      fi; \
      echo ">>> Mirror failed: $MIRROR, next..."; \
      rm -rf /var/lib/apt/lists/*; \
      sleep 5; \
    done; \
    if [ "$APT_OK" -ne 1 ]; then \
      echo "ERROR: all mirrors failed"; \
      exit 1; \
    fi; \
    for attempt in 1 2 3 4 5; do \
      if apt-get install -y --no-install-recommends \
        bash \
        ca-certificates \
        git \
        ripgrep \
        curl \
        wget \
        unzip \
        build-essential \
        cmake \
        pkg-config \
        libgtest-dev \
        python3 \
        python3-pip \
        python3-pytest \
        g++ \
        openjdk-17-jdk-headless \
        maven; then \
        if [ -d /usr/src/gtest ]; then \
          cd /usr/src/gtest && cmake . && make && cp lib/*.a /usr/lib/; \
        fi; \
        python3 -m pytest --version; \
        rm -rf /var/lib/apt/lists/*; \
        exit 0; \
      fi; \
      echo "apt-get install failed on attempt ${attempt}, retrying in $((attempt * 10))s..."; \
      apt-get -f install -y --no-install-recommends || true; \
      dpkg --configure -a || true; \
      rm -rf /var/lib/apt/lists/*; \
      sleep $((attempt * 10)); \
      apt-get -o Acquire::Retries=5 -o Acquire::http::Timeout=60 update || true; \
    done; \
    echo "ERROR: apt-get install failed after 5 attempts"; \
    exit 1

# 安装 Go，保持与 agent 基础镜像预期的主版本一致。
ARG GO_VERSION=1.24.2
ARG GO_DOWNLOAD_URLS=
RUN set -eux; \
    tarball="go${GO_VERSION}.linux-amd64.tar.gz"; \
    dest="/tmp/go.tar.gz"; \
    urls="${GO_DOWNLOAD_URLS:-https://mirrors.aliyun.com/golang/${tarball} https://go.dev/dl/${tarball} https://dl.google.com/go/${tarball}}"; \
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
ENV JAVA_HOME=/usr/lib/jvm/java-17-openjdk-amd64

RUN useradd -m -s /bin/bash agent \
    && mkdir -p /home/agent/.npm /home/agent/.cache /workspace \
    && chown -R agent:agent /home/agent /workspace

WORKDIR /workspace
ENV PATH="/usr/local/go/bin:/usr/local/bin:${PATH}"
