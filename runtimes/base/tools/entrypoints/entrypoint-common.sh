#!/usr/bin/env bash
# entrypoint-common.sh — Secret materializer for LLMSafeSpace pods.
# Reads /sandbox-cfg/secrets.json and materializes secrets to correct paths.
# All secrets are written to tmpfs (never to PVC).
set -euo pipefail

SECRETS_FILE="/sandbox-cfg/secrets.json"
ENV_FILE="/tmp/secrets-env"
# ↑ These paths must match pkg/agentd/types.go constants (SecretsEnvPath, AgentConfigPath, PasswordPath)
SSH_DIR="$HOME/.ssh"

# If no secrets.json exists, write empty config
if [[ ! -f "$SECRETS_FILE" ]]; then
    echo '{}' > /tmp/agent-config.json
    return 0 2>/dev/null || true
fi

# Initialize
echo '{}' > /tmp/agent-config.json
: > "$ENV_FILE"
mkdir -p "$SSH_DIR"
chmod 700 "$SSH_DIR"

# Parse secrets.json — array of {type, name, metadata, plaintext}
SECRET_COUNT=$(jq -r 'length' "$SECRETS_FILE")

for i in $(seq 0 $((SECRET_COUNT - 1))); do
    TYPE=$(jq -r ".[$i].type" "$SECRETS_FILE")
    NAME=$(jq -r ".[$i].name" "$SECRETS_FILE")
    PLAINTEXT=$(jq -r ".[$i].plaintext" "$SECRETS_FILE")
    METADATA=$(jq -c ".[$i].metadata // {}" "$SECRETS_FILE")

    case "$TYPE" in
        llm-provider)
            echo "$PLAINTEXT" > /tmp/agent-config.json
            ;;

        ssh-key)
            KEY_TYPE=$(echo "$METADATA" | jq -r '.key_type // "ed25519"')
            HOST=$(echo "$METADATA" | jq -r '.host // "github.com"')
            KEY_PATH="$SSH_DIR/id_${KEY_TYPE}_${NAME}"

            echo "$PLAINTEXT" > "$KEY_PATH"
            chmod 600 "$KEY_PATH"

            # Add host entry to ssh config
            cat >> "$SSH_DIR/config" <<EOF
Host $HOST
    IdentityFile $KEY_PATH
    StrictHostKeyChecking accept-new
EOF
            ;;

        git-credential)
            HOST=$(echo "$METADATA" | jq -r '.host // "github.com"')
            PROTOCOL=$(echo "$METADATA" | jq -r '.protocol // "https"')

            # Append to git-credentials file
            echo "${PROTOCOL}://oauth2:${PLAINTEXT}@${HOST}" >> "$HOME/.git-credentials"
            git config --global credential.helper store 2>/dev/null || true
            ;;

        secret-file)
            MOUNT_PATH=$(echo "$METADATA" | jq -r '.mount_path')
            if [[ -n "$MOUNT_PATH" && "$MOUNT_PATH" != "null" ]]; then
                # Force secret files under safe tmpfs directory
                SAFE_PATH="$HOME/.secrets/${MOUNT_PATH##*/home/sandbox/.secrets/}"
                SAFE_PATH="${SAFE_PATH//\.\.\//}"
                mkdir -p "$(dirname "$SAFE_PATH")"
                echo "$PLAINTEXT" > "$SAFE_PATH"
                chmod 600 "$SAFE_PATH"
            fi
            ;;

        env-secret)
            VAR_NAME=$(echo "$METADATA" | jq -r '.var_name')
            if [[ -n "$VAR_NAME" && "$VAR_NAME" != "null" ]]; then
                echo "export ${VAR_NAME}='${PLAINTEXT}'" >> "$ENV_FILE"
            fi
            ;;
    esac
done

# Set ssh config permissions
if [[ -f "$SSH_DIR/config" ]]; then
    chmod 600 "$SSH_DIR/config"
fi

# Set git-credentials permissions
if [[ -f "$HOME/.git-credentials" ]]; then
    chmod 600 "$HOME/.git-credentials"
fi

# Source env file for current shell
if [[ -s "$ENV_FILE" ]]; then
    chmod 600 "$ENV_FILE"
fi
