FROM ghcr.io/project-osrm/osrm-backend:v5.27.1

RUN apt-get update && apt-get install -y wget curl && \
    curl -fsSL https://deb.nodesource.com/setup_22.x | bash - && \
    apt-get install -y nodejs && \
    rm -rf /var/lib/apt/lists/*
RUN corepack enable && corepack prepare pnpm@latest --activate

WORKDIR /app

COPY pnpm-workspace.yaml pnpm-lock.yaml ./
COPY api/package*.json ./api/
COPY cloudflare/package*.json ./cloudflare/
RUN echo 'enable-pre-post-scripts=true' > .npmrc
RUN pnpm install --frozen-lockfile

COPY api/tsconfig.json ./api/
COPY api/src/ ./api/src/
RUN pnpm --filter osrm-api build

COPY profiles/ /profiles/
COPY cloudflare/start.sh /start.sh
RUN chmod +x /start.sh

EXPOSE 3000

CMD ["/start.sh"]
