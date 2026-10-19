# syncerity

`sbsync` bidirectionally syncs a project's markdown files with a folder in a [SilverBullet](https://silverbullet.md) space (for example `silverbullet.example.com`).

## Installation

```
go build -o sbsync .
```

Requires Go 1.22 or later. No external dependencies.

## Configuration

`sbsync init` creates `.sbsync.json` in the project directory:

```json
{
  "url": "https://silverbullet.example.com",
  "remotePrefix": "Proyectos/syncerity",
  "localDir": ".",
  "exclude": []
}
```

- `url`: SilverBullet address.
- `remotePrefix`: folder in the space that holds the project's markdown files. Nothing outside it is read or modified.
- `localDir`: root of the markdown files, relative to the project directory.
- `exclude`: directory names to ignore. `.git` and `node_modules` are always ignored.

If the server requires a token, set it in the `SB_TOKEN` environment variable and it is sent as `Authorization: Bearer`. If it is not set, nothing is sent.

## Usage

```
sbsync -C <project> init
sbsync -C <project> status
sbsync -C <project> sync
sbsync -C <project> -prefer remote sync
```

`status` is a dry run: it lists what `sync` would do without writing anything. Run it before the first `sync`.

## How it decides what to do

The repo is the source of truth, but anything created only in SilverBullet is not lost. **`sbsync` never deletes anything, locally or remotely**: the result is the union of both sides.

For each `.md` file, three hashes are compared: local, remote, and the one from the last successful sync, stored in `.sbsync-state.json`.

| Situation | Action |
|---|---|
| Identical on both sides | nothing |
| Exists only in the repo | `PUSH` to SilverBullet |
| Exists only in SilverBullet | `PULL` to the repo |
| Changed only in the repo | `PUSH` |
| Changed only in SilverBullet | `PULL` |
| Changed on both sides (conflict) | repo wins (`PUSH`); with `-prefer remote`, SilverBullet wins |

Consequence: deleting a file on one side does not delete it on the other, and the next `sync` recreates it. To remove it entirely, delete it in both places.

## Protocol

Uses SilverBullet's `/.fs` API: `GET /.fs` to list, and `GET` and `PUT` on `/.fs/<path>` for each file. Only names ending in `.md` are considered.

## Limitations

- Markdown only. Other files in the space (PDFs, images) are ignored.
- Downloads every remote file under the prefix on each run. This is fine for small spaces.
- No content merging: on a conflict, one whole side is chosen.
- Deletions are not propagated, by design.
- If `remotePrefix` differs only by case from an existing folder, or if paths differ only by case, the run aborts without writing anything.
- `.sbsync-state.json` and `sbsync` (the binary) must not be version-controlled.

## Development

```
go vet ./... && go test ./...
```

Tests use a mock SilverBullet server held in memory.
