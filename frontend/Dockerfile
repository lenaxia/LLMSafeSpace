FROM node:22-bookworm-slim AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . .
RUN npm run build

# nginxinc/nginx-unprivileged runs as UID 101 (nginx) and listens on
# 8080 by default. This avoids Trivy DS-0002 (running as root) and
# matches the security posture of the controller pods. The chart
# deployment templates already expose port 8080 internally; the
# Service maps 80 -> 8080.
FROM nginxinc/nginx-unprivileged:1.27-alpine
COPY --from=build /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY docker-entrypoint.sh /docker-entrypoint.d/40-envsubst-env-json.sh
USER root
RUN chmod +x /docker-entrypoint.d/40-envsubst-env-json.sh
USER nginx
EXPOSE 8080
