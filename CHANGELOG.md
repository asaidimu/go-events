# Changelog

## 2.0.0 (2026-06-29)

### Added
- `SubscribeOptions.LiveOnly` — skip Pebble catch-up for real-time consumers
- `SubscribeOptions.LiveBufferSize` — configurable in-memory channel capacity
- `SimpleEventBus[T]` — minimal generic wrapper with auto-generated subscriber IDs
- `SimpleConfig` — configure LiveOnly and buffer size at the bus level
- In-memory fast path: live events delivered via buffered channel, bypassing Pebble read+decode
- Wakeup broadcast via channel close-and-recreate for instant drain notification

### Changed
- Module path: `github.com/asaidimu/go-events` → `github.com/asaidimu/go-events/v2`
- `NewSimple` accepts optional `SimpleConfig` variadic argument

### Fixed
- Duplicate delivery when the same event was dispatched from both Pebble and the live channel (drain live channel after Pebble dispatch)

### Removed
- `UUIDForTime` now part of the compactor package (use `events.UUIDForTime`)

# [1.1.0](https://github.com/asaidimu/go-events/compare/v1.0.0...v1.1.0) (2025-06-18)

### Features

* **core:** add comprehensive async processing features and docs ([67545f2](https://github.com/asaidimu/go-events/commit/67545f2af4f6f8ef1fdc2d9161d6f59c7c6f3c65))

## 1.0.0 (2025-06-14)

* feat(bus)!: introduce in-memory event bus with full documentation ([fb30552](https://github.com/asaidimu/go-events/commit/fb3055295dfaf61b5783364deebfdd58f03a31c6))


### BREAKING CHANGES

* This commit entirely replaces the repository's previous placeholder "hello world" project with the functional go-events library. All prior code in main.go and pkg has been removed. Users upgrading from the previous state must adopt the new go-events API.
