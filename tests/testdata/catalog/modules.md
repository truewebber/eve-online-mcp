## Conventions

| Parameter | Description |
|---|---|
| `confirm_token` | Leave empty on the first call. |

### `eve_fitting_save`

Save a ship fitting.

| Parameter | Type | Required | Bounds | Description |
|---|---|---|---|---|
| `name` | string | **yes** | — | Fitting name as it will appear in game. |
| `modules` | object list | **yes** | — | Modules as objects with name, flag, quantity. |
| `confirm_token` | string | no | — | shared |

Each `modules` entry:

| Field | Type | Required | Description |
|---|---|---|---|
| `name` | string | **yes** | Exact module name. |
| `flag` | string | no | HiSlot0-7. |
| `quantity` | int | no | Default 1. |
