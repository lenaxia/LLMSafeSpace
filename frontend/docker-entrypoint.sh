#!/bin/sh
# Inject runtime environment variables at container start

# env.json for the SPA
cat > /usr/share/nginx/html/env.json <<EOF
{
  "apiBaseUrl": "${API_BASE_URL:-/api/v1}"
}
EOF

# Substitute API_UPSTREAM in nginx.conf
export API_UPSTREAM="${API_UPSTREAM:-api:8080}"
envsubst '${API_UPSTREAM}' < /etc/nginx/conf.d/default.conf.template > /etc/nginx/conf.d/default.conf
