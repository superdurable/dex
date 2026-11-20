# Build context: this directory (examples/typescript/codec-server).
# Node.js 24 runs the TypeScript sources directly by stripping types, so there is no build step.
FROM node:24-slim
ENV NODE_ENV=production
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --omit=dev --no-audit --no-fund && npm cache clean --force
COPY src/ ./src/
# The node user (uid 1000) ships with the image; the server never runs as root.
USER node
EXPOSE 8080
ENTRYPOINT ["node", "src/main.ts"]
