FROM python:3.12-slim-bookworm

LABEL description="NotebookLM MCP Server for goclaw (Python-based)"

# Install notebooklm-mcp-cli (includes both 'nlm' CLI and 'notebooklm-mcp' server)
RUN pip install --no-cache-dir notebooklm-mcp-cli

# Auth/profile data — mount from host so auth survives container restarts
VOLUME /root/.notebooklm-mcp-cli

EXPOSE 8000

HEALTHCHECK --interval=30s --timeout=10s --start-period=15s --retries=3 \
  CMD python3 -c "import urllib.request; urllib.request.urlopen('http://localhost:8000/health'); exit(0)" 2>/dev/null || exit 1

# Streamable HTTP transport — goclaw connects via http://notebooklm:8000/mcp
ENTRYPOINT ["notebooklm-mcp"]
CMD ["--transport", "http", "--port", "8000", "--host", "0.0.0.0"]
