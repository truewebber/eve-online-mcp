## Conventions

| Parameter | Description |
|---|---|
| `limit` | Maximum rows to return. Keep it small — every row costs context. Results say truncated when more exist. |
| `confirm_token` | Leave empty on the first call. |
| `page` | Which page of results to fetch, starting at 1. |
| `offset` | Skip this many rows of the result before returning any. |

## Server instructions

```
Hello from the contract.
```

### Pagination by tool

| Shape | How a caller walks it | Tools |
|---|---|---|
| **Cursor** — the endpoint pages by id | pass the cursor back from `next_cursor` | `eve_mail_list` (`last_mail_id`) |
| **Numbered pages** — the endpoint pages by number | `page` | `eve_assets_blueprints` |
| **Folded output** — the tool groups | `offset` | `eve_assets_list` |
| **One response, no paging** | filters, not pages | `eve_server_status` |

### `eve_server_status`

*Source: `internal/usecase/eve/account.go`*

Tranquility server status: player count, build version, uptime, VIP mode.

Returns: server_version, players.

_No parameters._

### `eve_assets_list`

*Source: `internal/usecase/eve/assets.go`*

Assets grouped by station.

| Parameter | Type | Required | Bounds | Description |
|---|---|---|---|---|
| `location` | string | no | — | Case-insensitive substring of a station name. |
| `min_value` | float64 | no | ≥ 0 | Hide locations holding less than this many ISK. |
| `limit` | int | no | 1–500 | shared |
| `offset` | int | no | ≥ 0 | shared |
| `items` | int | no | 1–200 | Maximum items per location. |

### `eve_mail_list`

*Source: `internal/usecase/eve/social.go`*

Mail headers only.

| Parameter | Type | Required | Bounds | Description |
|---|---|---|---|---|
| `last_mail_id` | int | no | ≥ 1 | Continue after this id. |
| `confirm_token` | string | no | — | shared |

### `eve_assets_blueprints`

*Source: `internal/usecase/eve/assets.go`*

Blueprints.

| Parameter | Type | Required | Bounds | Description |
|---|---|---|---|---|
| `page` | int | no | ≥ 1 | Which page of results to fetch, starting at 1. |
