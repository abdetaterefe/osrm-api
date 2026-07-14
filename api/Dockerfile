FROM node:20-alpine
RUN corepack enable && corepack prepare pnpm@latest --activate
WORKDIR /app
COPY package*.json pnpm-workspace.yaml ./
COPY api/package*.json ./api/
RUN pnpm install --frozen-lockfile
COPY api/tsconfig.json ./api/
COPY api/src/ ./api/src/
RUN pnpm --filter osrm-api build
EXPOSE 3000
CMD ["pnpm", "--filter", "osrm-api", "start"]
