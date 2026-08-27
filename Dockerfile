FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=1 \
    EVE_DATA_DIR=/data \
    EVE_HOST=0.0.0.0 \
    EVE_PORT=8765

WORKDIR /app

COPY requirements.txt pyproject.toml ./
COPY eve_mcp ./eve_mcp
# Locked tree first, then the package itself without re-resolving anything.
RUN pip install --no-cache-dir -r requirements.txt \
    && pip install --no-cache-dir --no-deps . \
    && useradd --system --uid 10001 --create-home eve \
    && mkdir -p /data && chown eve:eve /data

USER eve
VOLUME ["/data"]
EXPOSE 8765

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD python -c "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://127.0.0.1:8765/health', timeout=4).status==200 else 1)"

ENTRYPOINT ["python", "-m", "eve_mcp"]
