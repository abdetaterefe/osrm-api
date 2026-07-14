FROM node:20-alpine
RUN corepack enable && corepack prepare pnpm@latest --activate
WORKDIR /app
COPY pnpm-workspace.yaml pnpm-lock.yaml ./
COPY api/package*.json ./api/
COPY cloudflare/package*.json ./cloudflare/
RUN pnpm install --frozen-lockfile
COPY api/tsconfig.json ./api/
COPY api/src/ ./api/src/
RUN pnpm --filter osrm-api build
EXPOSE 3000
CMD ["node", "api/dist/server.js"]
