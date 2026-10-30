FROM node:22-slim

LABEL description="Google Docs MCP sidecar for goclaw with dependencies pre-installed (no apt/npx at container start)"

# Pre-install everything the old `command:` line fetched on EVERY start
# (apt python3/pip + mcp-proxy + the MCP package). Container start becomes
# instant and no longer depends on npm registry availability.
RUN apt-get update -qq && \
    apt-get install -y -qq python3 python3-pip && \
    pip install --break-system-packages -q mcp-proxy && \
    npm i -g @a-bonus/google-docs-mcp && \
    apt-get clean && rm -rf /var/lib/apt/lists/*

EXPOSE 8000

# npx --no-install resolves the package bin from the pre-installed global
# tree and fails fast instead of downloading (cold-start-proof).
ENTRYPOINT ["sh", "-c", "mcp-proxy --port 8000 --host 0.0.0.0 --pass-environment -- npx --no-install @a-bonus/google-docs-mcp"]
