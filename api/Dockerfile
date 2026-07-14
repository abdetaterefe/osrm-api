FROM node:20-alpine
WORKDIR /app
COPY api/package*.json ./
RUN npm install --production
COPY api/tsconfig.json ./
COPY api/src/ ./src/
RUN npx tsc
EXPOSE 3000
CMD ["node", "dist/server.js"]
