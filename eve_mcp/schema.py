"""Shared parameter annotations.

Every tool parameter carries a `Field(description=...)` so the description
lands in the JSON Schema the model actually reads, not only in the prose
description. Numeric bounds go in the schema too, so the model learns the
valid range up front instead of discovering it through an error.
"""
from __future__ import annotations

from typing import Annotated, Literal

from pydantic import Field

#: Which character to act on. Deliberately accepts either form; the name says so.
CharacterArg = Annotated[
    str,
    Field(
        description=(
            "Character name (e.g. 'Jane Doe') or numeric character id. "
            "Leave empty to use the only authorized character; required when "
            "several are authorized — call eve_auth_status to list them."
        )
    ),
]

Detail = Literal["concise", "detailed"]

DetailArg = Annotated[
    Detail,
    Field(
        description=(
            "'concise' (default) returns only the high-signal fields and costs "
            "far fewer tokens. Use 'detailed' when you need secondary fields and "
            "raw ids — it can be several times larger."
        )
    ),
]

ConfirmTokenArg = Annotated[
    str,
    Field(
        description=(
            "Leave empty on the first call: the tool returns a preview of exactly "
            "what it would do plus a single-use token. Show that preview to the "
            "user, get an explicit yes, then call again with identical arguments "
            "and the token here."
        )
    ),
]


def limit_arg(what: str, maximum: int = 500) -> object:
    """Build a documented, bounded `limit` annotation for one tool."""
    return Annotated[
        int,
        Field(
            description=(
                f"Maximum {what} to return. Keep it small — every row costs "
                f"context. Results say `truncated` when more exist."
            ),
            ge=1,
            le=maximum,
        ),
    ]
