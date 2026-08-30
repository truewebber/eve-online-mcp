# T13 — In-memory caches; drop the cache tables

- Status: `todo`
- Size: M
- Depends on: T12
- SPEC: §5.1, §12.5; DB.md "No cache tables"

## Goal

The ESI response cache, the id→name cache and the reference-price blob
move from Postgres into bounded pod memory, and `http_cache`, `names`
and `blobs` are dropped. Postgres is for state that must be correct
across replicas; a cache is neither.

Bounds are constants, not env (SPEC §2): responses 256 MiB **or** 2000
entries, whichever binds first, bodies over 8 MiB served but not stored;
id→name 50 000 ids; reference prices one blob, 1 h.

## Why this is one Composer session

It is `adapter/esi` + `adapter/names` + one migration, with no auth or
tool-contract surface. It also shrinks the schema before the identity
rewrite lands on top of it, so T14/T15 have less to think about.

## Do not

- Keep a Postgres fallback "for cross-pod hits". A cross-pod miss
  revalidates with the stored ETag and costs a 304, which costs nothing
  against CCP's error limit — that is the whole argument (SPEC §5.1).
- Cache a body over 8 MiB. One corp-assets sweep must not evict the hot
  set.
- Make the bounds configurable.
- Change TTL handling: `Expires` / `Cache-Control`, capped at 24 h,
  stays exactly as it is.
- Drop ETag revalidation. Without it this task is a regression.

## Context

`internal/adapter/store/cache.go` holds `CacheGet`, `CachePut`,
`CacheTouch`, `CachePurgeExpired`, `NameGet`, `NamePut`, `BlobGet`,
`BlobPut`, `PurgeExpired`. Every one of them goes away, along with the
three tables.

`adapter/esi` already routes all GETs through a cache lookup; the
replacement is an LRU with two ceilings (bytes and entries) behind the
same internal interface. `adapter/names` uses the names table and the
blob for `/markets/prices`.

Eviction accounting is the only subtle part: track stored bytes as
bodies are inserted and evicted, and treat "over 8 MiB" as a
serve-but-skip rather than an error.

## Work

1. Bounded LRU in `adapter/esi`: key → {ETag, body, headers, expiry},
   with byte and entry ceilings and the 8 MiB skip rule.
2. Bounded id→name map in `adapter/names` (50 000 ids, immutable data,
   so eviction order barely matters — LRU is fine).
3. Reference prices: one in-memory value with a 1 h TTL.
4. Delete the store methods above; migration dropping `http_cache`,
   `names`, `blobs`.
5. Tests against T11's fixtures: fresh hit serves without a network
   call; expired entry revalidates and a 304 refreshes the TTL; an
   oversized body is served and not stored; inserting past the entry
   ceiling evicts the oldest; the byte ceiling evicts before the entry
   ceiling when bodies are large.
6. Confirm the cache is shared per pod, not per character (SPEC §3.4):
   two characters requesting the same public endpoint hit once.

## Files

- Edit: `internal/adapter/esi/esi.go`, `internal/adapter/names/names.go`,
  `internal/usecase/session/session.go` (wiring, if it passes the store in)
- Delete: `internal/adapter/store/cache.go`
- Add: one migration

## Acceptance

- [ ] No table named `http_cache`, `names` or `blobs`, and no code
      referencing them
- [ ] Both ceilings enforced, with a test for each
- [ ] Oversized bodies served, never stored
- [ ] 304 revalidation still works and refreshes the TTL
- [ ] The response cache is shared across characters within a pod
- [ ] `go test ./...` passes

## Verify

```bash
rg -n 'http_cache|CacheGet|NamePut|BlobGet' --glob '*.go'
go test ./internal/adapter/esi ./internal/adapter/names -count=1
```

## Done

Set `Status: done` here and in [README.md](README.md).
