FROM ghcr.io/project-osrm/osrm-backend:v5.27.1

RUN apt-get update && apt-get install -y wget nodejs npm && rm -rf /var/lib/apt/lists/*

WORKDIR /app

COPY api/package*.json ./
RUN npm install --omit=dev

COPY api/server.js ./
COPY profiles/ /profiles/
COPY cloudflare/start.sh /start.sh
RUN chmod +x /start.sh

EXPOSE 3000

CMD ["/start.sh"]
