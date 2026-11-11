FROM python:3.11-slim-bookworm

LABEL description="Google Sheets MCP sidecar for goclaw with dependencies pre-installed (no pip install at container start)"

# Pre-install mcp-google-sheets and mcp<2 so container starts instantly
RUN pip install --no-cache-dir "mcp-google-sheets" "mcp<2"

COPY docker/mcp_sheets_wrapper.py /wrapper.py

EXPOSE 8000

CMD ["python3", "/wrapper.py"]
