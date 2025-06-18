# Dependency Catalog

## Peer Dependencies

### context (Go Standard Library)
- **Reason**: Required for passing context across handler calls, enabling cancellation and timeouts.
- **Version Requirements**: `Go 1.22+`

### log/slog (Go Standard Library)
- **Reason**: Required for structured, leveled logging throughout the event bus operations. Configurable via `EventBusConfig.Logger`.
- **Version Requirements**: `Go 1.22+`



---
*Generated using Gemini AI on 6/18/2025, 2:26:38 PM. Review and refine as needed.*