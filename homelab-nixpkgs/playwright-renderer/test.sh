#!/usr/bin/env bash
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONTAINER_NAME="pw-renderer-test"
PORT=3000
LLM_BASE="http://10.28.15.204"
VISION_MODEL="Qwen3-VL-30B-A3B-Instruct-UD-Q8_K_XL"
PNG_OUT="/tmp/playwright-renderer-test.png"

cleanup() {
    echo "==> Cleaning up container..."
    docker stop "$CONTAINER_NAME" 2>/dev/null || true
}
trap cleanup EXIT

# ── 1. Build ─────────────────────────────────────────────────────────────────
echo "==> Building docker image..."
cd "$SCRIPT_DIR"
SYSTEM=$(nix eval --raw --impure --expr 'builtins.currentSystem')
nix build ".#dockerImage.${SYSTEM}" --out-link /tmp/pw-renderer-result
echo "==> Loading image into docker..."
docker load < /tmp/pw-renderer-result

# ── 2. Start container ────────────────────────────────────────────────────────
echo "==> Starting container on port $PORT..."
docker stop "$CONTAINER_NAME" 2>/dev/null || true
docker run -d --rm \
    --name "$CONTAINER_NAME" \
    -p "${PORT}:${PORT}" \
    playwright-renderer:latest

# wait for service to be ready
echo -n "==> Waiting for service"
for i in $(seq 1 30); do
    if curl -sf "http://localhost:${PORT}/health" >/dev/null 2>&1; then
        echo " ready."
        break
    fi
    echo -n "."
    sleep 1
    if [ "$i" -eq 30 ]; then
        echo " timed out!"
        docker logs "$CONTAINER_NAME"
        exit 1
    fi
done

# ── 3. Render test HTML ───────────────────────────────────────────────────────
echo "==> Sending render request..."
cat > /tmp/pw-render-request.json << 'JSONEOF'
{
  "html": "<html><body style=\"margin:0;background:#0000cc\"><h1 style=\"color:#ff0000;font-size:80px;font-family:sans-serif;padding:40px\">PLAYWRIGHT TEST</h1><p style=\"color:white;font-size:40px;font-family:sans-serif;padding:40px\">HTML rendering via Playwright</p></body></html>",
  "width": 800,
  "height": 600
}
JSONEOF

HTTP_STATUS=$(curl -s -o "$PNG_OUT" -w "%{http_code}" \
    -X POST "http://localhost:${PORT}/render" \
    -H "Content-Type: application/json" \
    -d @/tmp/pw-render-request.json)

if [ "$HTTP_STATUS" != "200" ]; then
    echo "ERROR: render endpoint returned HTTP $HTTP_STATUS"
    cat "$PNG_OUT"
    exit 1
fi

PNG_SIZE=$(wc -c < "$PNG_OUT")
echo "==> Received PNG: $PNG_SIZE bytes -> $PNG_OUT"

if [ "$PNG_SIZE" -lt 1000 ]; then
    echo "ERROR: PNG is suspiciously small ($PNG_SIZE bytes)"
    exit 1
fi

# ── 4. Vision LLM verification ────────────────────────────────────────────────
echo "==> Encoding image for vision model..."
B64=$(base64 -w 0 "$PNG_OUT")

echo "==> Querying $VISION_MODEL for image description..."
RESPONSE=$(curl -s -X POST "${LLM_BASE}/v1/chat/completions" \
    -H "Content-Type: application/json" \
    -d "{
        \"model\": \"${VISION_MODEL}\",
        \"max_tokens\": 300,
        \"messages\": [
            {
                \"role\": \"user\",
                \"content\": [
                    {
                        \"type\": \"image_url\",
                        \"image_url\": {\"url\": \"data:image/png;base64,${B64}\"}
                    },
                    {
                        \"type\": \"text\",
                        \"text\": \"Describe this image in one sentence. What text does it contain and what are the colors?\"
                    }
                ]
            }
        ]
    }")

LLM_TEXT=$(echo "$RESPONSE" | jq -r '.choices[0].message.content // empty')
if [ -z "$LLM_TEXT" ]; then
    echo "ERROR: no content in LLM response"
    echo "Raw response: $RESPONSE"
    docker stop "$CONTAINER_NAME" 2>/dev/null || true
    exit 1
fi

echo ""
echo "==> Vision model response:"
echo "    $LLM_TEXT"
echo ""

# ── 5. Assert ─────────────────────────────────────────────────────────────────
LOWER=$(echo "$LLM_TEXT" | tr '[:upper:]' '[:lower:]')

PASS=true
if echo "$LOWER" | grep -qiE "playwright|test|red|blue|html"; then
    echo "PASS: LLM response mentions expected content."
else
    echo "WARN: LLM response did not clearly mention expected keywords (playwright, test, red, blue, html)."
    echo "      Full response: $LLM_TEXT"
    PASS=false
fi

if $PASS; then
    echo ""
    echo "==> All checks passed."
else
    echo ""
    echo "==> Test completed with warnings - review LLM response above."
    exit 1
fi
