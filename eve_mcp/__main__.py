"""Container entrypoint: wire the context, mount the app, serve."""
from __future__ import annotations

import contextlib
import logging
import os
import sys

import uvicorn

from .config import load_settings
from .context import build_context, close_context
from .server import build_app, build_mcp


def _configure_logging() -> None:
    logging.basicConfig(
        level=os.environ.get("EVE_LOG_LEVEL", "INFO").upper(),
        format="%(asctime)s %(levelname)-7s %(name)s: %(message)s",
        stream=sys.stderr,
    )
    logging.getLogger("httpx").setLevel(logging.WARNING)


async def _serve() -> None:
    settings = load_settings()
    log = logging.getLogger("eve_mcp")

    ctx = await build_context(settings)
    mcp = build_mcp(ctx)
    app = build_app(ctx, mcp)

    if settings.bearer_token:
        log.info("bearer token required for %s", settings.mcp_path)
    elif settings.host not in ("127.0.0.1", "localhost"):
        # Inside Docker this is normal: compose publishes the port on loopback.
        # It only matters if the published port is reachable from elsewhere.
        log.info(
            "listening on %s with no EVE_MCP_TOKEN set — fine when the published port "
            "is bound to 127.0.0.1, otherwise set a token",
            settings.host,
        )

    authorized = [t.character_name for t in ctx.sso.store.all()]
    log.info("write mode: %s (%s)", settings.write_mode,
             ", ".join(sorted(settings.write_allow)) or "nothing enabled")
    log.info("authorized characters: %s", ", ".join(authorized) or "none — open /auth/login")
    log.info("MCP endpoint: http://%s:%s%s", settings.host, settings.port, settings.mcp_path)

    config = uvicorn.Config(
        app,
        host=settings.host,
        port=settings.port,
        log_level=os.environ.get("EVE_LOG_LEVEL", "info").lower(),
        access_log=False,
    )
    server = uvicorn.Server(config)
    try:
        await server.serve()
    finally:
        await close_context(ctx)


def main() -> None:
    _configure_logging()
    import anyio

    with contextlib.suppress(KeyboardInterrupt):
        anyio.run(_serve)


if __name__ == "__main__":
    main()
